package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"waves/internal/config"
	"waves/internal/mcp"
)

// hookTimeout is the hook's own deadline; Claude Code's timeout (5 s) must stay longer.
const hookTimeout = 500 * time.Millisecond

// hook is the Claude Code PreToolUse entry point. Exit 0 allows, exit 2
// denies. Under enforce, every failure denies; under audit, nothing blocks.
func (e *Env) hook(ctx context.Context, args []string) (err error) {
	if len(args) == 2 && args[0] == "--signal" {
		return e.hookSignal(ctx, args[1])
	}
	policy := "enforce"
	if len(args) == 2 && args[0] == "--policy" {
		policy = args[1]
	}
	fail := func(reason string) error {
		if policy == "enforce" {
			fmt.Fprintln(e.Err, "waves guard: "+reason)
			return exitCode(2)
		}
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fail("internal error")
		}
	}()
	if policy == "off" {
		return nil
	}
	// Claude Code lets the call through when its own 5 s timeout fires, so the
	// hook decides itself well before: deny under enforce after 3 s whatever stalls.
	deadline := time.AfterFunc(3*time.Second, func() {
		if policy == "enforce" {
			fmt.Fprintln(e.Err, "waves guard: timed out")
			os.Exit(2)
		}
		os.Exit(0)
	})
	defer deadline.Stop()
	body, rerr := io.ReadAll(io.LimitReader(e.In, 1<<20+1))
	if rerr != nil || len(body) > 1<<20 {
		return fail("could not read the hook event")
	}
	cfg, cerr := config.Load(e.Dirs)
	tok, terr := config.ReadSecret(e.Dirs.GuardToken())
	if cerr != nil || terr != nil {
		return fail("guard token or config missing; run waves setup")
	}
	c, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, "POST", "http://"+cfg.Addr()+"/api/v1/guard/decide", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	res, herr := http.DefaultClient.Do(req)
	if herr != nil {
		return fail("the Waves service did not answer")
	}
	defer res.Body.Close()
	var d struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason_code"`
	}
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&d) != nil {
		return fail(fmt.Sprintf("bad answer from the service (HTTP %d)", res.StatusCode))
	}
	switch d.Decision {
	case "allow":
		return nil
	case "deny":
		fmt.Fprintf(e.Err, "waves guard denied this tool call (%s). %s\n", d.Reason, denyAdvice[d.Reason])
		return exitCode(2)
	}
	return fail("unknown decision")
}

// hookSignal is the Stop and UserPromptSubmit entry point. It only tells Waves that a turn ended or began; it never
// decides anything, never blocks (always exit 0, nothing on stdout) and never waits long, so it cannot hold up Claude Code.
func (e *Env) hookSignal(ctx context.Context, event string) (err error) {
	defer func() {
		if recover() != nil {
			err = nil // exit 2 on a Stop hook would make Claude Code keep going
		}
	}()
	if event != "stop" && event != "prompt" && event != "end" {
		return nil
	}
	body, rerr := io.ReadAll(io.LimitReader(e.In, 1<<20+1))
	if rerr != nil || len(body) > 1<<20 {
		return nil
	}
	var in struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(body, &in) != nil || in.SessionID == "" {
		return nil
	}
	cfg, cerr := config.Load(e.Dirs)
	tok, terr := config.ReadSecret(e.Dirs.GuardToken())
	if cerr != nil || terr != nil {
		return nil
	}
	out, _ := json.Marshal(map[string]string{"session_id": in.SessionID, "event": event})
	c, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(c, "POST", "http://"+cfg.Addr()+"/api/v1/guard/signal", bytes.NewReader(out))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	if res, err := http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
	}
	return nil
}

// mcpBridge runs the stdio MCP bridge for one project.
func (e *Env) mcpBridge(ctx context.Context, args []string) error {
	if len(args) != 2 || args[0] != "--project" {
		return errors.New("usage: waves mcp --project <project id>")
	}
	cfg, err := config.Load(e.Dirs)
	if err != nil {
		return err
	}
	tok, err := config.ReadSecret(filepath.Join(e.Dirs.Config, "mcp", filepath.Base(args[1])+".token"))
	if err != nil {
		return fmt.Errorf("no MCP credential for this project; connect it with: waves agent connect claude-code (%w)", err)
	}
	return mcp.Bridge(ctx, "http://"+cfg.Addr()+"/mcp", tok, Version, &sdk.StdioTransport{})
}

// denyAdvice tells the agent what to do after a denial.
var denyAdvice = map[string]string{
	"lease_ended":       "Your lease ended. Stop and report to the human.",
	"read_only_task":    "This task is read-only. Do not write files.",
	"protected_path":    "Waves protects .git, .waves, the Claude settings and .mcp.json. Do not edit them.",
	"outside_workspace": "Write only inside the project folder.",
	"unmanaged":         "Claim a task with waves_claim_task first.",
	"no_target":         "The tool call names no file.",
	"error":             "Stop and report a blocker.",
}
