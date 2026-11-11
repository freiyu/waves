// Package config resolves Waves' directories (XDG base directories) and reads
// the small JSON configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// DefaultPort is the loopback port the service listens on.
const DefaultPort = 7317

// Dirs are the directories Waves owns. All are created with mode 0700.
type Dirs struct {
	Config string // config.json, operator.token, guard.token
	Data   string // waves.db, backups, context packages, diffs
	State  string // execution logs, validator output, pid file, service log
}

// Resolve returns the directories from XDG_CONFIG_HOME, XDG_DATA_HOME and
// XDG_STATE_HOME, with the usual ~/.config, ~/.local/share, ~/.local/state fallbacks.
func Resolve() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, fmt.Errorf("find home directory: %w", err)
	}
	pick := func(env, fallback string) string {
		if v := os.Getenv(env); filepath.IsAbs(v) {
			return filepath.Join(v, "waves")
		}
		return filepath.Join(home, fallback, "waves")
	}
	return Dirs{
		Config: pick("XDG_CONFIG_HOME", ".config"),
		Data:   pick("XDG_DATA_HOME", filepath.Join(".local", "share")),
		State:  pick("XDG_STATE_HOME", filepath.Join(".local", "state")),
	}, nil
}

// Ensure creates the directories with mode 0700 and tightens existing ones.
func (d Dirs) Ensure() error {
	for _, p := range []string{d.Config, d.Data, d.State} {
		if err := os.MkdirAll(p, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", p, err)
		}
		if err := os.Chmod(p, 0o700); err != nil {
			return fmt.Errorf("restrict %s: %w", p, err)
		}
	}
	return nil
}

func (d Dirs) DB() string            { return filepath.Join(d.Data, "waves.db") }
func (d Dirs) OperatorToken() string { return filepath.Join(d.Config, "operator.token") }
func (d Dirs) GuardToken() string    { return filepath.Join(d.Config, "guard.token") }
func (d Dirs) File() string          { return filepath.Join(d.Config, "config.json") }
func (d Dirs) PIDFile() string       { return filepath.Join(d.State, "waves.pid") }
func (d Dirs) Logs() string          { return filepath.Join(d.State, "logs") }
func (d Dirs) Backups() string       { return filepath.Join(d.Data, "backups") }
func (d Dirs) Artifacts() string     { return filepath.Join(d.Data, "artifacts") }

// File is the on-disk configuration. Unknown keys are refused.
type File struct {
	Port int `json:"port"`
}

// Load reads config.json; a missing file gives the defaults.
func Load(d Dirs) (File, error) {
	f := File{Port: DefaultPort}
	b, err := os.ReadFile(d.File())
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, fmt.Errorf("read config: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return f, fmt.Errorf("config %s: %w", d.File(), err)
	}
	if f.Port < 1 || f.Port > 65535 {
		return f, fmt.Errorf("config %s: port %d is out of range", d.File(), f.Port)
	}
	return f, nil
}

// Save writes config.json with mode 0600.
func Save(d Dirs, f File) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return WriteSecret(d.File(), append(b, '\n'))
}

// Addr is the loopback listen address.
func (f File) Addr() string { return "127.0.0.1:" + strconv.Itoa(f.Port) }

// WriteSecret writes a file with mode 0600 atomically.
func WriteSecret(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// ReadSecret reads a token file and refuses it if group or others can read it.
func ReadSecret(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by other users (mode %o); run chmod 600 %s", path, st.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return string(b), nil
}
