package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"

	"golang.org/x/term"

	"waves/internal/config"
	"waves/internal/core"
)

// readSecretTTY reads a passphrase from the terminal without echo. It refuses
// anything that is not an interactive terminal: the passphrase is never read
// from pipes, files, arguments or the environment.
func (e *Env) readSecretTTY(prompt string) (string, error) {
	fd := int(e.In.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("this needs an interactive terminal; the passphrase is never read from a pipe, a file or the environment")
	}
	fmt.Fprint(e.Err, prompt)
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(e.Err)
	if err != nil {
		return "", fmt.Errorf("read passphrase: %w", err)
	}
	return string(b), nil
}

func (e *Env) setup(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return errUsage
	}
	if !term.IsTerminal(int(e.In.Fd())) {
		return errors.New("waves setup needs an interactive terminal to set the approval passphrase")
	}
	if err := e.Dirs.Ensure(); err != nil {
		return err
	}
	cfg, err := config.Load(e.Dirs)
	if err != nil {
		return err
	}
	if _, err := os.Stat(e.Dirs.File()); errors.Is(err, os.ErrNotExist) {
		if err := config.Save(e.Dirs, cfg); err != nil {
			return err
		}
	}
	c, db, bus, err := openCore(ctx, e.Dirs, slog.New(slog.DiscardHandler))
	if err != nil {
		return err
	}
	defer db.Close()
	defer bus.Close()
	for _, t := range []struct{ scope, path string }{{"operator", e.Dirs.OperatorToken()}, {"guard", e.Dirs.GuardToken()}} {
		if _, err := os.Stat(t.path); err == nil {
			fmt.Fprintf(e.Out, "kept      %s token  %s\n", t.scope, t.path)
			continue
		}
		if err := c.RevokeCredentials(ctx, t.scope); err != nil {
			return err
		}
		tok, err := c.CreateCredential(ctx, t.scope)
		if err != nil {
			return err
		}
		if err := config.WriteSecret(t.path, []byte(tok+"\n")); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "created   %s token  %s (mode 600)\n", t.scope, t.path)
	}
	if c.PassphraseSet(ctx) {
		fmt.Fprintln(e.Out, "kept      approval passphrase (rotate it with: waves auth passphrase)")
	} else {
		fmt.Fprintf(e.Out, "\nChoose the approval passphrase. Every approval asks for it. At least %d characters.\n", core.MinPassphraseLen)
		fmt.Fprintln(e.Out, "Agents run as your user and can read files Waves writes, so use a passphrase you do not store on this machine.")
		for {
			p1, err := e.readSecretTTY("Passphrase: ")
			if err != nil {
				return err
			}
			if err := core.CheckPassphraseStrength(p1); err != nil {
				fmt.Fprintln(e.Err, "  "+err.Error()+". Try again.")
				continue
			}
			p2, err := e.readSecretTTY("Again:      ")
			if err != nil {
				return err
			}
			if p1 != p2 {
				fmt.Fprintln(e.Err, "  The two entries differ. Try again.")
				continue
			}
			if err := c.SetInitialPassphrase(ctx, p1); err != nil {
				return err
			}
			break
		}
		fmt.Fprintln(e.Out, "set       approval passphrase")
	}
	fmt.Fprintf(e.Out, "database  %s\n", e.Dirs.DB())
	fmt.Fprintf(e.Out, "address   http://%s (loopback only)\n", cfg.Addr())
	for _, bin := range []string{"git", "claude"} {
		if p, err := exec.LookPath(bin); err == nil {
			fmt.Fprintf(e.Out, "found     %-7s %s\n", bin, p)
		} else {
			fmt.Fprintf(e.Out, "missing   %-7s not on PATH\n", bin)
		}
	}
	fmt.Fprintln(e.Out, "\nNext: waves start, then waves open.")
	return nil
}
