package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"waves/internal/store"
)

// Setting bounds. Keys in securityKeys need a human elevation to change.
var settingDefaults = map[string]int{
	"elevation_window_min": 5,
	"max_concurrent":       2,
	"hard_cap":             8,
	"heartbeat_interval_s": 30,
	"log_retention_days":   14,
	"log_max_mb":           50,
	"session_ttl_h":        12,
}

var settingRange = map[string][2]int{
	"elevation_window_min": {0, 30},
	"max_concurrent":       {1, 0}, // upper bound is hard_cap
	"heartbeat_interval_s": {15, 300},
	"log_retention_days":   {1, 365},
}

var securityKeys = map[string]bool{"elevation_window_min": true, "max_concurrent": true}

func (s *Service) settingInt(ctx context.Context, key string) int {
	var v string
	err := s.db.R.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&v)
	if err == nil {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return settingDefaults[key]
}

// SettingsView returns every setting with its current value.
func (s *Service) SettingsView(ctx context.Context) (map[string]int, string, int) {
	out := map[string]int{}
	for k := range settingDefaults {
		out[k] = s.settingInt(ctx, k)
	}
	var at sql.NullString
	var size sql.NullInt64
	s.db.R.QueryRowContext(ctx, `SELECT ts, json_extract(detail_json,'$.bytes') FROM audit WHERE event = 'backup.created' ORDER BY seq DESC LIMIT 1`).Scan(&at, &size)
	return out, at.String, int(size.Int64)
}

// PatchSettings changes the given keys. Unknown keys and out-of-range values
// are refused; security keys need a human elevation.
func (s *Service) PatchSettings(ctx context.Context, p Principal, elevation string, changes map[string]int) error {
	if !p.IsOperator() {
		return errWrongScope
	}
	needHuman := false
	for k, v := range changes {
		r, ok := settingRange[k]
		if !ok {
			return errValidation(fmt.Sprintf("The setting %q cannot be changed here.", k), "Use the documented settings only.")
		}
		hi := r[1]
		if k == "max_concurrent" {
			hi = s.settingInt(ctx, "hard_cap")
		}
		if v < r[0] || v > hi {
			return errValidation(fmt.Sprintf("%s must be %d to %d.", k, r[0], hi), "Use a value in the allowed range.")
		}
		needHuman = needHuman || securityKeys[k]
	}
	if len(changes) == 0 {
		return nil
	}
	return s.tx(ctx, func(t *Tx) error {
		scope := "operator"
		if needHuman {
			if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
				return err
			}
			scope = "human"
		}
		for k, v := range changes {
			if _, err := t.ExecContext(ctx, `INSERT INTO settings(key, value_json, updated_at) VALUES (?,?,?) ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
				k, strconv.Itoa(v), s.ts()); err != nil {
				return wrap(err, "store setting %s", k)
			}
		}
		b, _ := json.Marshal(changes)
		return t.audit(ctx, store.AuditRecord{Event: "settings.changed", ActorScope: scope, ActorRef: p.ActorRef(), Detail: map[string]any{"changes": string(b)}})
	})
}

// Backup writes an online backup into the backup directory and nowhere else:
// an operator credential must not be able to make the service create a file
// at a path of its choosing. `waves backup <path>` copies the result itself.
func (s *Service) Backup(ctx context.Context, p Principal) (string, int64, error) {
	if !p.IsOperator() {
		return "", 0, errWrongScope
	}
	dst := filepath.Join(s.dirs.Backups(), "waves-"+s.now().UTC().Format("20060102T150405.000Z")+".db")
	n, err := s.db.Backup(ctx, dst)
	if errors.Is(err, os.ErrExist) {
		return "", 0, errInvalidState("A backup with this name already exists.", "Try again in a moment.")
	}
	if err != nil {
		return "", 0, err
	}
	err = s.Audit(ctx, store.AuditRecord{Event: "backup.created", ActorScope: "operator", ActorRef: p.ActorRef(), Detail: map[string]any{"bytes": n, "file": filepath.Base(dst)}})
	return dst, n, err
}

// GC applies log retention: logs of executions that ended more than
// log_retention_days ago, and old validator output, are deleted. Databases,
// diffs and context packages are kept. Explicit and audited.
func (s *Service) GC(ctx context.Context, p Principal) (int, int64, error) {
	if !p.IsOperator() {
		return 0, 0, errWrongScope
	}
	cut := s.now().Add(-time.Duration(s.settingInt(ctx, "log_retention_days")) * 24 * time.Hour)
	rows, err := s.db.R.QueryContext(ctx, `SELECT log_path FROM executions WHERE log_path IS NOT NULL AND state IN ('Completed','Failed','Cancelled') AND ended_at < ?`, store.FormatTime(cut))
	if err != nil {
		return 0, 0, wrap(err, "find old logs")
	}
	var paths []string
	for rows.Next() {
		var pth string
		rows.Scan(&pth)
		paths = append(paths, pth)
	}
	rows.Close()
	if vs, err := filepath.Glob(filepath.Join(s.dirs.Logs(), "validators", "*.log")); err == nil {
		for _, v := range vs {
			if st, err := os.Stat(v); err == nil && st.ModTime().Before(cut) {
				paths = append(paths, v)
			}
		}
	}
	n, freed := 0, int64(0)
	for _, pth := range paths {
		// Only files inside the Waves log folder are ever removed.
		if rel, err := filepath.Rel(s.dirs.Logs(), pth); err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		if st, err := os.Stat(pth); err == nil && os.Remove(pth) == nil {
			n++
			freed += st.Size()
		}
	}
	return n, freed, s.Audit(ctx, store.AuditRecord{Event: "gc.ran", ActorScope: "operator", ActorRef: p.ActorRef(), Detail: map[string]any{"files": n, "bytes": freed}})
}
