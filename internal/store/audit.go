package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// GenesisHash is the prev_hash of the first audit record.
const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// AuditRecord is one audit entry. Detail must never hold secrets.
type AuditRecord struct {
	TS         string
	Event      string
	ActorScope string // system, agent, guard, operator, human
	ActorRef   string
	ProjectID  string
	Subject    string
	Detail     map[string]any
}

// canonical is the hashed form: fixed field order, detail as stored.
type canonical struct {
	TS         string          `json:"ts"`
	Event      string          `json:"event"`
	ActorScope string          `json:"actor_scope"`
	ActorRef   string          `json:"actor_ref"`
	ProjectID  string          `json:"project_id"`
	Subject    string          `json:"subject"`
	Detail     json.RawMessage `json:"detail"`
}

func chainHash(prev string, c canonical) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("encode audit record: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(prev))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Execer is satisfied by *sql.Tx and *sql.DB.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// AppendAudit adds a record to the chain inside tx and returns its seq.
func AppendAudit(ctx context.Context, tx Execer, r AuditRecord) (int64, error) {
	if r.TS == "" {
		r.TS = Now()
	}
	detail := []byte("{}")
	if len(r.Detail) > 0 {
		b, err := json.Marshal(r.Detail)
		if err != nil {
			return 0, fmt.Errorf("encode audit detail: %w", err)
		}
		detail = b
	}
	var prev string
	err := tx.QueryRowContext(ctx, `SELECT hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		prev = GenesisHash
	} else if err != nil {
		return 0, fmt.Errorf("read audit head: %w", err)
	}
	c := canonical{r.TS, r.Event, r.ActorScope, r.ActorRef, r.ProjectID, r.Subject, detail}
	hash, err := chainHash(prev, c)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO audit(ts, event, actor_scope, actor_ref, project_id, subject, detail_json, prev_hash, hash)
		VALUES (?,?,?,?,?,?,?,?,?)`, r.TS, r.Event, r.ActorScope, nullStr(r.ActorRef), nullStr(r.ProjectID), nullStr(r.Subject), string(detail), prev, hash)
	if err != nil {
		return 0, fmt.Errorf("append audit %s: %w", r.Event, err)
	}
	return res.LastInsertId()
}

// ChainStatus is the result of a verification.
type ChainStatus struct {
	OK       bool
	Records  int
	BrokenAt int64   // seq of the first record that does not verify
	Breaks   []int64 // every record that does not verify (at most 100)
	Head     string
	HeadSeq  int64
}

// VerifyAudit walks the whole chain and recomputes every hash.
func VerifyAudit(ctx context.Context, q Execer) (ChainStatus, error) {
	rows, err := q.QueryContext(ctx, `SELECT seq, ts, event, actor_scope, COALESCE(actor_ref,''), COALESCE(project_id,''), COALESCE(subject,''), detail_json, prev_hash, hash FROM audit ORDER BY seq`)
	if err != nil {
		return ChainStatus{}, fmt.Errorf("read audit: %w", err)
	}
	defer rows.Close()
	st := ChainStatus{OK: true, Head: GenesisHash}
	prev := GenesisHash
	var lastSeq int64
	for rows.Next() {
		var seq int64
		var c canonical
		var detail, prevHash, hash string
		if err := rows.Scan(&seq, &c.TS, &c.Event, &c.ActorScope, &c.ActorRef, &c.ProjectID, &c.Subject, &detail, &prevHash, &hash); err != nil {
			return ChainStatus{}, fmt.Errorf("scan audit: %w", err)
		}
		st.Records++
		c.Detail = json.RawMessage(detail)
		want, err := chainHash(prev, c)
		// A gap in seq means a record was removed.
		if err != nil || prevHash != prev || hash != want || (lastSeq != 0 && seq != lastSeq+1) {
			if st.OK {
				st.OK = false
				st.BrokenAt = seq
			}
			if len(st.Breaks) < 100 {
				st.Breaks = append(st.Breaks, seq)
			}
		}
		prev = hash
		lastSeq = seq
		st.Head, st.HeadSeq = hash, seq
	}
	if err := rows.Err(); err != nil {
		return ChainStatus{}, fmt.Errorf("read audit: %w", err)
	}
	return st, nil
}

// CheckpointMAC is the HMAC written at a human step-up over (seq, hash).
func CheckpointMAC(key []byte, seq int64, hash string) string {
	m := hmac.New(sha256.New, key)
	fmt.Fprintf(m, "%d:%s", seq, hash)
	return hex.EncodeToString(m.Sum(nil))
}

// VerifyCheckpoints checks every stored checkpoint against key and the
// current chain. It returns the first failing checkpoint id, or 0.
func VerifyCheckpoints(ctx context.Context, q Execer, key []byte) (int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.id, c.seq, c.hash, c.hmac, COALESCE(a.hash,'') FROM audit_checkpoints c LEFT JOIN audit a ON a.seq = c.seq ORDER BY c.id`)
	if err != nil {
		return 0, fmt.Errorf("read checkpoints: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int64
		var hash, mac, cur string
		if err := rows.Scan(&id, &seq, &hash, &mac, &cur); err != nil {
			return 0, fmt.Errorf("scan checkpoint: %w", err)
		}
		if cur != hash || !hmac.Equal([]byte(mac), []byte(CheckpointMAC(key, seq, hash))) {
			return id, nil
		}
	}
	return 0, rows.Err()
}

// WriteCheckpoint stores a checkpoint for the current head.
func WriteCheckpoint(ctx context.Context, tx Execer, key []byte) error {
	var seq int64
	var hash string
	err := tx.QueryRowContext(ctx, `SELECT seq, hash FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&seq, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read audit head: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_checkpoints(seq, hash, hmac, created_at) VALUES (?,?,?,?)`, seq, hash, CheckpointMAC(key, seq, hash), Now())
	if err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

func nullStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// NullStr turns "" into SQL NULL.
func NullStr(s string) any { return nullStr(s) }
