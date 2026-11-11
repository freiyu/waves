// Package store owns the SQLite database: opening it with the required
// pragmas, versioned migrations, the tamper-evident audit chain, and backups.
//
// There is one writer connection (transactions start with BEGIN IMMEDIATE) and
// a small pool of read-only connections.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver, registers "sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNewerSchema means the database was written by a newer Waves binary.
var ErrNewerSchema = errors.New("the database schema is newer than this binary")

// DB holds the writer and the reader pools.
type DB struct {
	W    *sql.DB // single connection, BEGIN IMMEDIATE
	R    *sql.DB // read-only connections
	Path string
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".sql")
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("migration %s: bad version prefix", e.Name())
		}
		b, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		sum := sha256.Sum256(b)
		out = append(out, migration{v, name, string(b), hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1..n without gaps, found %d at position %d", m.version, i+1)
		}
	}
	return out, nil
}

// SchemaVersion is the newest migration this binary knows.
func SchemaVersion() int {
	ms, err := loadMigrations()
	if err != nil {
		return 0
	}
	return len(ms)
}

func dsn(path string, write bool) string {
	q := url.Values{}
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	if write {
		q.Add("_pragma", "journal_mode(WAL)")
		q.Add("_pragma", "synchronous(NORMAL)")
		q.Set("_txlock", "immediate")
	} else {
		q.Add("_pragma", "query_only(1)")
	}
	return fileURI(path, q)
}

// fileURI builds a SQLite URI that survives a path holding ? # % or spaces.
func fileURI(path string, q url.Values) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs // a relative path would be read as a URI host
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

// Open opens (creating if needed) the database at path and applies pending
// migrations. Before migrating an existing database it writes a backup next
// to it. It refuses a database with a newer schema or with foreign keys off.
func Open(ctx context.Context, path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	w, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	w.SetConnMaxIdleTime(0)
	if err := checkForeignKeys(ctx, w); err != nil {
		w.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		w.Close()
		return nil, fmt.Errorf("restrict database file: %w", err)
	}
	if err := migrate(ctx, w, path); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	r.SetMaxOpenConns(4)
	if err := checkForeignKeys(ctx, r); err != nil {
		w.Close()
		r.Close()
		return nil, err
	}
	return &DB{W: w, R: r, Path: path}, nil
}

func checkForeignKeys(ctx context.Context, db *sql.DB) error {
	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("read foreign_keys pragma: %w", err)
	}
	if fk != 1 {
		return errors.New("SQLite foreign keys are off; refusing to start")
	}
	return nil
}

func migrate(ctx context.Context, w *sql.DB, path string) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	return migrateWith(ctx, w, path, ms)
}

func migrateWith(ctx context.Context, w *sql.DB, path string, ms []migration) error {
	var have int
	err := w.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&have)
	if err != nil {
		return fmt.Errorf("inspect schema: %w", err)
	}
	applied := map[int]string{}
	if have == 1 {
		rows, err := w.QueryContext(ctx, `SELECT version, checksum FROM schema_migrations`)
		if err != nil {
			return fmt.Errorf("read schema_migrations: %w", err)
		}
		for rows.Next() {
			var v int
			var c string
			if err := rows.Scan(&v, &c); err != nil {
				rows.Close()
				return fmt.Errorf("scan schema_migrations: %w", err)
			}
			applied[v] = c
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("read schema_migrations: %w", err)
		}
	}
	for v := range applied {
		if v > len(ms) {
			return fmt.Errorf("%w: database is at version %d, this binary knows %d; use a newer waves", ErrNewerSchema, v, len(ms))
		}
	}
	var pending []migration
	for _, m := range ms {
		c, ok := applied[m.version]
		if !ok {
			pending = append(pending, m)
			continue
		}
		if c != m.checksum {
			return fmt.Errorf("migration %s was changed after it was applied; refusing to start", m.name)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if len(applied) > 0 {
		bak := fmt.Sprintf("%s.pre-v%d-%s.bak", path, pending[0].version, time.Now().UTC().Format("20060102T150405Z"))
		if err := precreate(bak); err != nil {
			return fmt.Errorf("create backup before migrating: %w", err)
		}
		if _, err := w.ExecContext(ctx, `VACUUM INTO ?`, bak); err != nil {
			os.Remove(bak)
			return fmt.Errorf("backup before migrating: %w", err)
		}
	}
	for _, m := range pending {
		if err := applyMigration(ctx, w, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(ctx context.Context, w *sql.DB, m migration) error {
	tx, err := w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.name, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?,?,?,?)`,
		m.version, m.name, m.checksum, Now()); err != nil {
		return fmt.Errorf("record migration %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.name, err)
	}
	return nil
}

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.R.Close(), d.W.Close())
}

// Tx runs fn in one write transaction (BEGIN IMMEDIATE) and commits if fn
// returns nil.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Backup writes a consistent copy of the live database to dst (VACUUM INTO)
// and returns its size.
func (d *DB) Backup(ctx context.Context, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return 0, fmt.Errorf("create backup dir: %w", err)
	}
	if err := precreate(dst); err != nil {
		if errors.Is(err, os.ErrExist) {
			return 0, fmt.Errorf("backup target %s already exists: %w", dst, os.ErrExist)
		}
		return 0, fmt.Errorf("create backup file: %w", err)
	}
	if _, err := d.W.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		os.Remove(dst)
		return 0, fmt.Errorf("backup: %w", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		return 0, fmt.Errorf("stat backup: %w", err)
	}
	return st.Size(), nil
}

// RestoreInfo says what a restore did, for the audit record.
type RestoreInfo struct {
	SHA256   string // of the backup file that was restored
	Replaced string // where the previous database was kept, "" if there was none
}

// Restore replaces the database file at path with the backup at src. The
// service must be stopped. The backup is copied once, and the copy is what is
// checked (it must open, pass integrity_check and carry a schema this binary
// knows), what receives the `db.restored` audit record, and what is moved into
// place, so the file cannot change between the check and the use and a restore
// is never without its record. The replaced database is kept as
// path.replaced-<time>.
func Restore(ctx context.Context, src, path string) (RestoreInfo, error) {
	var info RestoreInfo
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return info, fmt.Errorf("open backup: %w", err)
	}
	defer in.Close()
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() {
		return info, fmt.Errorf("%s is not a regular file", src)
	}
	// Write the restored copy first, so a crash at any point leaves a database
	// at path: the old one, or the restored one.
	tmp := path + ".restoring"
	removeWorking(tmp) // leftovers of an earlier crash
	defer removeWorking(tmp)
	sum, err := copySynced(tmp, in)
	if err != nil {
		return info, fmt.Errorf("copy backup: %w", err)
	}
	info.SHA256 = sum
	if err := verifyBackup(ctx, tmp); err != nil {
		return info, err
	}
	if _, err := os.Stat(path); err == nil {
		keep, err := keepCurrent(ctx, path)
		if err != nil {
			return info, fmt.Errorf("keep current database: %w", err)
		}
		info.Replaced = keep
	}
	if err := appendRestoreRecord(ctx, tmp, info); err != nil {
		return info, fmt.Errorf("record the restore: %w", err)
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		os.Remove(tmp + sfx) // belong to the working copy, not to the database that moves into place
		if err := os.Remove(path + sfx); err != nil && !errors.Is(err, os.ErrNotExist) {
			return info, fmt.Errorf("remove %s: %w", sfx, err)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		return info, fmt.Errorf("move restored database: %w", err)
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		d.Sync()
		d.Close()
	}
	return info, nil
}

// removeWorking deletes a working copy and the sidecar files SQLite may have made for it.
func removeWorking(tmp string) {
	for _, sfx := range []string{"", "-wal", "-shm"} {
		os.Remove(tmp + sfx)
	}
}

// keepCurrent saves the database being replaced as <path>.replaced-<time>. It folds the WAL into the main file
// first. A damaged database, which is the usual reason to restore, cannot be checkpointed: then the main file
// and its WAL are copied as they are.
func keepCurrent(ctx context.Context, path string) (string, error) {
	keep := path + ".replaced-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := checkpoint(ctx, path); err == nil {
		return keep, copyFile(path, keep)
	}
	if err := copyFile(path, keep); err != nil {
		return "", err
	}
	if _, err := os.Stat(path + "-wal"); err == nil {
		if err := copyFile(path+"-wal", keep+"-wal"); err != nil {
			return "", err
		}
	}
	return keep, nil
}

// appendRestoreRecord adds the audit record of the restore to the verified copy, before it goes into place.
// The restored file brings its own passphrase, approvals and audit log; this record, and the alert it raises
// until a human acknowledges it, are how the human notices.
func appendRestoreRecord(ctx context.Context, file string, info RestoreInfo) error {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	db, err := sql.Open("sqlite", fileURI(file, q))
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	detail := map[string]any{"method": "tty", "source_sha256": info.SHA256}
	if info.Replaced != "" {
		detail["replaced_copy"] = filepath.Base(info.Replaced)
	}
	if _, err := AppendAudit(ctx, tx, AuditRecord{Event: "db.restored", ActorScope: "local_offline", Detail: detail}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// verifyBackup opens the file read-only and checks it is a Waves database this binary can use.
func verifyBackup(ctx context.Context, file string) error {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	probe, err := sql.Open("sqlite", fileURI(file, q))
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer probe.Close()
	var ic string
	if err := probe.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ic); err != nil {
		return fmt.Errorf("check backup: %w", err)
	}
	if ic != "ok" {
		return fmt.Errorf("backup failed integrity_check: %s", ic)
	}
	var v int
	if err := probe.QueryRowContext(ctx, `SELECT COALESCE(max(version),0) FROM schema_migrations`).Scan(&v); err != nil {
		return fmt.Errorf("backup has no schema_migrations table: %w", err)
	}
	if v > SchemaVersion() {
		return fmt.Errorf("%w: backup is at version %d", ErrNewerSchema, v)
	}
	return nil
}

// precreate makes a new empty private file: never over an existing file, never through a symlink.
// VACUUM INTO accepts an empty target, so a backup is private from its first byte.
func precreate(dst string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}

// copySynced creates a new private file from r (never through a symlink, never over an existing file),
// syncs it and returns the SHA-256 of what it wrote.
func copySynced(path string, r io.Reader) (string, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// checkpoint writes the WAL of the database at path into the main file and
// empties it.
func checkpoint(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", fileURI(path, nil))
	if err != nil {
		return err
	}
	defer db.Close()
	var busy, logFrames, done int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &done); err != nil {
		return err
	}
	if busy != 0 {
		return errors.New("the database is in use")
	}
	return nil
}

// writeSynced creates a new file (never through a symlink, never over an
// existing file) and syncs it.
func writeSynced(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeSynced(dst, b)
}

// Now is the timestamp format used in every table: RFC 3339 UTC with
// milliseconds, so text order is time order.
func Now() string { return FormatTime(time.Now()) }

// FormatTime formats t like Now.
func FormatTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// ParseTime parses a stored timestamp; the zero time on error.
func ParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
