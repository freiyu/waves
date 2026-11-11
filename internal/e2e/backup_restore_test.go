//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"waves/internal/api/contract"
)

// An operator credential must not make the service create a file at a path of its choosing. The service writes
// into its own backup folder; `waves backup <path>` copies the file as the user.
func TestBackupPathIsNotChosenByTheService(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	evil := filepath.Join(i.dir, "elsewhere", "evil.db")
	if st, _ := i.operator().call("POST", "/backup", map[string]string{"path": evil}, nil, nil); st != 422 {
		t.Fatalf("a backup path chosen through the API: %d", st)
	}
	if _, err := os.Stat(evil); err == nil {
		t.Fatal("the service created a file at the path the API caller chose")
	}
	var r struct {
		Path string `json:"path"`
	}
	i.operator().must("POST", "/backup", map[string]string{}, &r)
	if !strings.HasPrefix(r.Path, filepath.Join(i.dataDir(), "backups")+string(filepath.Separator)) {
		t.Fatalf("backup outside the backup folder: %s", r.Path)
	}
	if st, err := os.Stat(r.Path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode: %v %v", st, err)
	}
	// The CLI copies to the path you name, privately, and refuses to overwrite.
	dst := filepath.Join(i.dir, "safe", "out.db")
	if out, code := i.run("", "backup", dst); code != 0 {
		t.Fatalf("waves backup <path>: exit %d\n%s", code, out)
	}
	if st, err := os.Stat(dst); err != nil || st.Mode().Perm() != 0o600 || st.Size() == 0 {
		t.Fatalf("copied backup: %v %v", st, err)
	}
	if out, code := i.run("", "backup", dst); code == 0 || !strings.Contains(out, "backups") {
		t.Fatalf("a second backup over the same path: exit %d\n%s", code, out)
	}
	victim := filepath.Join(i.dir, "victim.txt")
	os.WriteFile(victim, []byte("keep"), 0o600)
	link := filepath.Join(i.dir, "link.db")
	if err := os.Symlink(victim, link); err != nil {
		t.Fatal(err)
	}
	if _, code := i.run("", "backup", link); code == 0 {
		t.Fatal("waves backup followed a symlink")
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("the file behind the symlink changed: %q", b)
	}
}

// Restore trusts the file completely. Through the real binary: a refused file changes nothing; a restored one is
// recorded as local_offline with the file's hash, stays on the attention list until a human acknowledges it,
// and no other credential can clear it.
func TestRestoreIsRecordedAndStaysAnAlert(t *testing.T) {
	t.Parallel()
	i := newInst(t)
	bak := filepath.Join(i.dir, "safe", "good.db")
	if out, code := i.run("", "backup", bak); code != 0 {
		t.Fatalf("backup: %d %s", code, out)
	}
	i.stop()
	garbage := filepath.Join(i.dir, "garbage.db")
	os.WriteFile(garbage, []byte("not a database"), 0o600)
	before, _ := os.ReadFile(i.dbPath())
	if _, err := i.tty([]string{"restore", garbage}, "restore\n"); err == nil {
		t.Fatal("restore accepted a file that is not a database")
	}
	if now, _ := os.ReadFile(i.dbPath()); string(now) != string(before) {
		t.Fatal("a refused restore changed the database")
	}
	if m, _ := filepath.Glob(i.dbPath() + ".restoring"); len(m) != 0 {
		t.Fatalf("a refused restore left %v", m)
	}
	if out, err := i.tty([]string{"restore", bak}, "restore\n"); err != nil || !strings.Contains(out, "Restored") {
		t.Fatalf("restore: %v\n%s", err, out)
	}
	raw, _ := os.ReadFile(bak)
	sum := sha256.Sum256(raw)
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'db.restored' AND actor_scope = 'local_offline' AND detail_json LIKE ?`, "%"+hex.EncodeToString(sum[:])+"%"); n != 1 {
		t.Fatalf("restore records naming the file's hash: %d", n)
	}
	if n := i.count(`SELECT count(*) FROM audit WHERE event = 'db.restored' AND actor_scope = 'human'`); n != 0 {
		t.Fatal("a restore was recorded as a human action")
	}
	i.start()
	listed := func() bool {
		var st contract.StatusResponse
		i.operator().must("GET", "/status", nil, &st)
		for _, a := range st.NeedsAttention {
			if a.Kind == "database_restored" {
				return true
			}
		}
		return false
	}
	if !listed() {
		t.Fatal("the restore is not on the attention list")
	}
	dash := i.dashboard()
	elevation := dash.stepUp("")
	for name, c := range map[string]*client{"operator": i.operator(), "guard": i.as(i.guard)} {
		if st, _ := c.call("POST", "/audit/acknowledge", nil, nil, elev(elevation)); st < 400 {
			t.Errorf("the %s token acknowledged the restore: %d", name, st)
		}
	}
	if !listed() {
		t.Fatal("a refused acknowledgement cleared the alert")
	}
	dash.must("POST", "/audit/acknowledge", nil, nil, elev(elevation))
	if listed() {
		t.Fatal("the alert stays after a human acknowledged it")
	}
}
