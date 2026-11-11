package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sqlTx = sql.Tx

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "waves.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAppliesMigrationAndPragmas(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	var n int
	if err := db.R.QueryRowContext(ctx, `SELECT count(*) FROM allowed_transitions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 105 {
		t.Fatalf("allowed_transitions rows = %d, want 105 (0001-0003: 104, 0004: task Done -> Ready by human)", n)
	}
	var mode string
	if err := db.W.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
	if _, err := db.R.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES ('x','y')`); err == nil {
		t.Fatal("reader pool accepted a write")
	}
	st, err := os.Stat(db.Path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("database mode = %v (%v), want 0600", st.Mode().Perm(), err)
	}
}

func TestReopenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waves.db")
	for i := 0; i < 2; i++ {
		db, err := Open(context.Background(), path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		db.Close()
	}
	m, _ := filepath.Glob(path + ".pre-*")
	if len(m) != 0 {
		t.Fatalf("backup written without a pending migration: %v", m)
	}
}

func TestRefusesNewerSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waves.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`INSERT INTO schema_migrations(version,name,checksum,applied_at) VALUES (99,'future','x','2030-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, err = Open(context.Background(), path)
	if !errors.Is(err, ErrNewerSchema) {
		t.Fatalf("open newer schema: err = %v, want ErrNewerSchema", err)
	}
}

func TestRefusesChangedMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waves.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE schema_migrations SET checksum='tampered' WHERE version=1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("err = %v, want changed-migration refusal", err)
	}
}

func TestBackupBeforeMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "waves.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	next := len(ms) + 1
	ms = append(ms, migration{version: next, name: "test", sql: `CREATE TABLE t2(x INTEGER);`, checksum: "c2"})
	if err := migrateWith(context.Background(), db.W, path, ms); err != nil {
		t.Fatal(err)
	}
	db.Close()
	m, _ := filepath.Glob(fmt.Sprintf("%s.pre-v%d-*.bak", path, next))
	if len(m) != 1 {
		t.Fatalf("backups = %v, want one backup before v%d", m, next)
	}
}

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "waves.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *sqlTx) error {
		_, err := AppendAudit(ctx, tx, AuditRecord{Event: "backup.created", ActorScope: "operator"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "b", "copy.db")
	n, err := db.Backup(ctx, bak)
	if err != nil || n == 0 {
		t.Fatalf("backup: %d %v", n, err)
	}
	if _, err := db.Backup(ctx, bak); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	// A change after the backup must be gone after restore.
	if err := db.Tx(ctx, func(tx *sqlTx) error {
		_, err := AppendAudit(ctx, tx, AuditRecord{Event: "after.backup", ActorScope: "operator"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	info, err := Restore(ctx, bak, path)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(info.SHA256) != 64 || info.Replaced == "" {
		t.Fatalf("restore info incomplete: %+v", info)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := VerifyAudit(ctx, db.R)
	if err != nil || !st.OK || st.Records != 2 {
		t.Fatalf("after restore: %+v %v, want 2 verified records (the backed-up one and db.restored; the one made after the backup is gone)", st, err)
	}
	if m, _ := filepath.Glob(path + ".replaced-*"); len(m) != 1 {
		t.Fatalf("replaced database not kept: %v", m)
	}
	bad := filepath.Join(dir, "garbage.db")
	os.WriteFile(bad, []byte("not a database"), 0o600)
	if _, err := Restore(ctx, bad, path); err == nil {
		t.Fatal("restore accepted a broken file")
	}
	if _, err := os.Stat(path + ".restoring"); err == nil {
		t.Fatal("a refused restore left its working copy behind")
	}
	if db2, err := Open(ctx, path); err != nil {
		t.Fatalf("a refused restore damaged the database: %v", err)
	} else {
		db2.Close()
	}
}

// The backup file is private from its first byte, and a path that is already taken or is a symlink is refused.
func TestBackupIsPrivateAndNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	dir := t.TempDir()
	dst := filepath.Join(dir, "sub", "b.db")
	if _, err := db.Backup(ctx, dst); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dst); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode: %v %v", st, err)
	}
	link := filepath.Join(dir, "link.db")
	victim := filepath.Join(dir, "victim.txt")
	os.WriteFile(victim, []byte("keep"), 0o600)
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Backup(ctx, link); err == nil {
		t.Fatal("backup followed a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("the file behind the symlink was changed: %q", b)
	}
	if _, err := db.Backup(ctx, victim); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
}

// Paths with characters that are special in a SQLite URI must open the database they name.
func TestOddPathsOpenTheRightFile(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"a?b.db", "a#b.db", "a%20b.db", "a b.db"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		db.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s: the database was not created at that path: %v", name, err)
		}
		bak := path + ".bak"
		db, _ = Open(ctx, path)
		if _, err := db.Backup(ctx, bak); err != nil {
			t.Fatalf("%s: backup: %v", name, err)
		}
		db.Close()
		if _, err := Restore(ctx, bak, path); err != nil {
			t.Fatalf("%s: restore: %v", name, err)
		}
	}
}

func TestAuditChainDetectsTampering(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	for _, ev := range []string{"a.one", "a.two", "a.three"} {
		if err := db.Tx(ctx, func(tx *sqlTx) error {
			_, err := AppendAudit(ctx, tx, AuditRecord{Event: ev, ActorScope: "system", Detail: map[string]any{"k": ev}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	st, err := VerifyAudit(ctx, db.R)
	if err != nil || !st.OK || st.Records != 3 {
		t.Fatalf("clean chain: %+v %v", st, err)
	}
	// The trigger refuses an update and a delete.
	if _, err := db.W.Exec(`UPDATE audit SET event='x' WHERE seq=2`); err == nil {
		t.Fatal("audit update accepted")
	}
	if _, err := db.W.Exec(`DELETE FROM audit WHERE seq=2`); err == nil {
		t.Fatal("audit delete accepted")
	}
	// A record with a wrong link is refused at insert.
	if _, err := db.W.Exec(`INSERT INTO audit(ts,event,actor_scope,detail_json,prev_hash,hash) VALUES ('t','x','system','{}','bad','bad2')`); err == nil {
		t.Fatal("unlinked audit insert accepted")
	}
	// Someone with direct file access drops the trigger and edits a record.
	if _, err := db.W.Exec(`DROP TRIGGER audit_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.Exec(`UPDATE audit SET detail_json='{"k":"forged"}' WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	st, err = VerifyAudit(ctx, db.R)
	if err != nil || st.OK || st.BrokenAt != 2 {
		t.Fatalf("tampered chain: %+v %v, want broken at 2", st, err)
	}
}

func TestAuditChainDetectsRemovedRecord(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	for i := 0; i < 3; i++ {
		db.Tx(ctx, func(tx *sqlTx) error {
			_, err := AppendAudit(ctx, tx, AuditRecord{Event: "e", ActorScope: "system"})
			return err
		})
	}
	db.W.Exec(`DROP TRIGGER audit_no_delete`)
	if _, err := db.W.Exec(`DELETE FROM audit WHERE seq=2`); err != nil {
		t.Fatal(err)
	}
	st, _ := VerifyAudit(ctx, db.R)
	if st.OK || st.BrokenAt != 3 {
		t.Fatalf("removed record: %+v, want broken at 3", st)
	}
}

func TestCheckpoints(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	key := []byte("0123456789abcdef0123456789abcdef")
	db.Tx(ctx, func(tx *sqlTx) error {
		_, err := AppendAudit(ctx, tx, AuditRecord{Event: "e", ActorScope: "human"})
		return err
	})
	if err := db.Tx(ctx, func(tx *sqlTx) error { return WriteCheckpoint(ctx, tx, key) }); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifyCheckpoints(ctx, db.R, key); err != nil || id != 0 {
		t.Fatalf("verify: %d %v", id, err)
	}
	if id, _ := VerifyCheckpoints(ctx, db.R, []byte("wrong key")); id == 0 {
		t.Fatal("checkpoint verified under a wrong key")
	}
}

func TestStateColumnNeedsTransition(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.W.ExecContext(ctx, q, a...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO projects(id,name,path,is_git,created_at) VALUES ('prj_1','p','/tmp/p',0,'t')`)
	exec(`INSERT INTO workflows(id,project_id,created_at) VALUES ('wf_1','prj_1','t')`)
	exec(`INSERT INTO workflow_versions(id,workflow_id,version_no,state,content_hash,created_at) VALUES ('wfv_1','wf_1',1,'draft','h','t')`)
	exec(`INSERT INTO phases(id,workflow_version_id,key,position,title,objective,updated_at) VALUES ('phs_1','wfv_1','P1',1,'t','o','t')`)
	if _, err := db.W.ExecContext(ctx, `UPDATE phases SET state='Approved' WHERE id='phs_1'`); err == nil {
		t.Fatal("direct phase state update accepted")
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO phases(id,workflow_version_id,key,position,title,objective,state,updated_at) VALUES ('phs_2','wfv_1','P2',2,'t','o','Approved','t')`); err == nil {
		t.Fatal("phase inserted in a non-initial state")
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO approvals(id,category,subject_type,subject_id,project_id,state,requested_by,requested_at,decided_at,decided_by_scope) VALUES ('apr_1','phase_start','phase','phs_1','prj_1','approved','system','t','t','operator')`); err == nil {
		t.Fatal("operator-decided approval accepted")
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO projects(id,name,path,is_git,created_at) VALUES ('prj_2','p','/tmp/q',0,'t'),('prj_3','p','/tmp/r',0,'t')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(ctx, `INSERT INTO requirements(id,project_id,kind,body,created_at) VALUES ('r','prj_missing','inline','x','t')`); err == nil {
		t.Fatal("foreign key not enforced")
	}
}

// A restore is never without its record: db.restored is written into the verified copy before it goes into
// place, the chain still verifies, and no working file is left behind.
func TestRestoreWritesItsOwnAuditRecord(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "waves.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Tx(ctx, func(tx *sqlTx) error {
		_, err := AppendAudit(ctx, tx, AuditRecord{Event: "first", ActorScope: "system"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "b.db")
	if _, err := db.Backup(ctx, bak); err != nil {
		t.Fatal(err)
	}
	db.Close()
	info, err := Restore(ctx, bak, path)
	if err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var event, scope, detail string
	if err := db.R.QueryRow(`SELECT event, actor_scope, detail_json FROM audit ORDER BY seq DESC LIMIT 1`).Scan(&event, &scope, &detail); err != nil {
		t.Fatal(err)
	}
	if event != "db.restored" || scope != "local_offline" || !strings.Contains(detail, info.SHA256) || !strings.Contains(detail, filepath.Base(info.Replaced)) || strings.Contains(detail, dir) {
		t.Fatalf("restore record: %s %s %s", event, scope, detail)
	}
	if st, err := VerifyAudit(ctx, db.R); err != nil || !st.OK || st.Records != 2 {
		t.Fatalf("chain after restore: %+v %v", st, err)
	}
	for _, sfx := range []string{".restoring", ".restoring-wal", ".restoring-shm"} {
		if _, err := os.Stat(path + sfx); err == nil {
			t.Errorf("%s left behind", sfx)
		}
	}
}

// The usual reason to restore is a damaged database: it must still be replaced, and what was there is kept.
func TestRestoreReplacesADamagedDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "waves.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	bak := filepath.Join(dir, "b.db")
	if _, err := db.Backup(ctx, bak); err != nil {
		t.Fatal(err)
	}
	db.Close()
	damaged := []byte("this is not a sqlite database, it was damaged")
	if err := os.WriteFile(path, damaged, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := Restore(ctx, bak, path)
	if err != nil {
		t.Fatalf("restore over a damaged database: %v", err)
	}
	if kept, _ := os.ReadFile(info.Replaced); string(kept) != string(damaged) {
		t.Fatalf("the damaged database was not kept: %q", kept)
	}
	db, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("restored database does not open: %v", err)
	}
	db.Close()
}

func TestFileURIResolvesRelativePaths(t *testing.T) {
	if u := fileURI("rel/x.db", nil); !strings.HasPrefix(u, "file:///") {
		t.Fatalf("a relative path became %q", u)
	}
}
