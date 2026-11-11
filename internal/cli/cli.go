// Package cli is the waves command line. Most commands call the HTTP API with
// the operator token; setup, restore, the passphrase reset, hook and mcp work
// without the service.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"waves/internal/config"
)

// usage is printed for `waves help` and unknown commands.
const usage = `waves: local orchestrator for coding agents, with mandatory human phase approval

Usage: waves <command> [arguments]

Service:
  setup                  create config, database, tokens and the approval passphrase (needs a terminal)
  serve                  run the service in the foreground
  start | stop | status  run in the background, stop it, show what needs you
  open                   print a one-time sign-in link for the dashboard (valid 60 s)
  doctor                 check the environment, database and audit chain

Work:
  project list | add <path> | switch <name> | remove <name>
  requirements add <file> | --text "<text>"
  plan request           create a planning request for the agent
  workflow show
  task list | retry <id> "<reason>" | reopen <id> "<reason>"
  execution list | logs <id> [-f] | cancel <id>
  approval list | review <id>
  agent list | connect claude-code | disconnect | status
  guard status | off | audit | enforce

Maintenance:
  backup [path] | restore <path>   restore needs the service stopped
  auth passphrase [--reset]        rotate the passphrase; --reset works offline only
  gc                               apply log retention

Agent side (called by Claude Code, not by you):
  mcp                    stdio bridge to the MCP endpoint
  hook                   guard hook entry point
`

// Run executes one command and returns the process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	d, err := config.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, "waves:", err)
		return 1
	}
	env := &Env{Dirs: d, Out: stdout, Err: stderr, In: os.Stdin}
	err = env.dispatch(ctx, args[0], args[1:])
	var ec exitCode
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ec):
		return int(ec)
	case errors.Is(err, errUsage):
		fmt.Fprint(stderr, usage)
		return 2
	default:
		fmt.Fprintln(stderr, "waves:", strings.TrimPrefix(err.Error(), "waves: "))
		return 1
	}
}

// Env carries the directories and the standard streams.
type Env struct {
	Dirs config.Dirs
	Out  io.Writer
	Err  io.Writer
	In   *os.File
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

var errUsage = errors.New("usage")

func (e *Env) dispatch(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(e.Out, usage)
		return nil
	case "version", "--version":
		fmt.Fprintln(e.Out, "waves", Version)
		return nil
	case "serve":
		return serve(ctx, e.Dirs)
	case "setup":
		return e.setup(ctx, args)
	}
	if fn, ok := e.commands()[cmd]; ok {
		return fn(ctx, args)
	}
	return errUsage
}
