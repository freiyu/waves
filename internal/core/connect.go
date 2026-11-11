package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"waves/internal/api/contract"
	"waves/internal/config"
	"waves/internal/store"
)

// Matcher is the PreToolUse matcher the guard hook installs.
const hookMatcher = "Write|Edit|MultiEdit|NotebookEdit|Bash|mcp__waves__.*"

// Capabilities of Claude Code as observed in headless mode.
var claudeCapabilities = []contract.Capability{
	{Name: "launch_execution", Value: "supported", Note: "claude -p with a prompt, tools and MCP config (Waves does not launch it in this version)"},
	{Name: "send_prompt", Value: "supported", Note: "prompt argument; --resume for follow-ups"},
	{Name: "receive_events", Value: "supported", Note: "stream-json events"},
	{Name: "stream_output", Value: "supported", Note: "stream-json"},
	{Name: "interrupt", Value: "partial", Note: "SIGINT ends cleanly; SIGKILL leaves tool processes running"},
	{Name: "resume_session", Value: "supported", Note: "--resume keeps the session id"},
	{Name: "retrieve_result", Value: "supported", Note: "result event"},
	{Name: "structured_output", Value: "unknown", Note: "not exercised"},
	{Name: "connect_mcp", Value: "supported", Note: "stdio and Streamable HTTP, project-level configuration"},
	{Name: "guard_hooks", Value: "partial", Note: "blocks only on exit 2; Waves wraps the hook so failures deny in enforce"},
	{Name: "report_usage", Value: "supported", Note: "usage and cost in the result event"},
}

// connectFiles are the two project files a connection edits.
type connectFiles struct {
	settings, mcp string
}

func filesOf(root string) connectFiles {
	return connectFiles{filepath.Join(root, ".claude", "settings.local.json"), filepath.Join(root, ".mcp.json")}
}

// wavesBinary is the absolute path of this binary, for the hook and MCP commands.
func wavesBinary() string {
	p, err := os.Executable()
	if err != nil {
		return "waves"
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// hookCommand is the installed hook. In enforce, /bin/sh turns any failure,
// including a missing binary, into exit 2 so Claude Code denies.
func hookCommand(bin, policy string) string {
	if policy == "enforce" {
		return "/bin/sh -c " + shQuote(shQuote(bin)+" hook --policy enforce || exit 2")
	}
	return shQuote(bin) + " hook --policy " + policy
}

// maxConnectFile bounds how much of a project's settings or MCP file Waves reads.
const maxConnectFile = 1 << 20

func readJSONObject(path string) (map[string]any, bool, error) {
	// A symlink here could point the edit at user-level configuration.
	for _, p := range []string{filepath.Dir(path), path} {
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return nil, true, fmt.Errorf("%s is a symbolic link; Waves edits only real project files", p)
		}
	}
	// The agent controls these files: a FIFO would block the open forever and a huge file would fill memory.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s is not a regular file", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxConnectFile+1))
	if err != nil {
		return nil, true, err
	}
	if len(b) > maxConnectFile {
		return nil, true, fmt.Errorf("%s is larger than %d KiB", path, maxConnectFile>>10)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil || m == nil {
		return nil, true, fmt.Errorf("%s is not a JSON object; fix or move it first", path)
	}
	return m, true, nil
}

func encodeJSON(m map[string]any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(m)
	return b.Bytes()
}

// planned computes both files after connecting with policy.
// hookEntry is the PreToolUse entry Waves installs.
func hookEntry(bin, policy string) map[string]any {
	return map[string]any{"matcher": hookMatcher, "hooks": []any{map[string]any{"type": "command", "command": hookCommand(bin, policy), "timeout": 5}}}
}

// hookDrift compares the hook in the project with the one Waves installs for
// policy. "" means the file still holds exactly that hook and hooks are not
// disabled; otherwise it says what is wrong. The guard is only as good as this
// file: an agent with a shell can edit it while a task is leased.
func hookDrift(root, policy string) string {
	sm, exists, err := readJSONObject(filesOf(root).settings)
	switch {
	case err != nil:
		return ".claude/settings.local.json cannot be read: " + err.Error()
	case !exists:
		return ".claude/settings.local.json is missing"
	}
	if off, _ := sm["disableAllHooks"].(bool); off {
		return "hooks are disabled in .claude/settings.local.json (disableAllHooks); remove that setting"
	}
	// A project set to off may still carry the audit hook it was connected with.
	pols := []string{policy}
	if policy == "off" {
		pols = append(pols, "audit")
	}
	var want []string
	for _, pol := range pols {
		b, _ := json.Marshal(hookEntry(wavesBinary(), pol))
		want = append(want, string(b))
	}
	hooks, _ := sm["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	found := false
	for _, e := range pre {
		if !isWavesHook(e) {
			continue
		}
		found = true
		got, _ := json.Marshal(e)
		if !slices.Contains(want, string(got)) {
			return "the Waves hook entry in .claude/settings.local.json was changed"
		}
	}
	if !found {
		return "the Waves hook entry is missing from .claude/settings.local.json"
	}
	return ""
}

func plannedEdits(root, project, policy string) (settings, mcpFile []byte, lines []string, err error) {
	f := filesOf(root)
	bin := wavesBinary()
	sm, _, err := readJSONObject(f.settings)
	if err != nil {
		return nil, nil, nil, err
	}
	mm, _, err := readJSONObject(f.mcp)
	if err != nil {
		return nil, nil, nil, err
	}
	entry := hookEntry(bin, policy)
	hooks, _ := sm["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	pre, _ := hooks["PreToolUse"].([]any)
	var kept []any
	for _, e := range pre {
		if !isWavesHook(e) {
			kept = append(kept, e)
		}
	}
	hooks["PreToolUse"] = append(kept, entry)
	sm["hooks"] = hooks
	en, _ := sm["enabledMcpjsonServers"].([]any)
	has := false
	for _, x := range en {
		has = has || x == "waves"
	}
	if !has {
		sm["enabledMcpjsonServers"] = append(en, "waves")
	}
	servers, _ := mm["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["waves"] = map[string]any{"type": "stdio", "command": bin, "args": []any{"mcp", "--project", project}}
	mm["mcpServers"] = servers
	settings, mcpFile = encodeJSON(sm), encodeJSON(mm)
	eb, _ := json.Marshal(entry)
	lines = []string{
		"# " + f.settings,
		`+ "hooks": { "PreToolUse": [ ` + string(eb) + ` ] }`,
		`+ "enabledMcpjsonServers": [ "waves" ]`,
		"# " + f.mcp,
		fmt.Sprintf(`+ "mcpServers": { "waves": { "type": "stdio", "command": %q, "args": ["mcp", "--project", %q] } }`, bin, project),
		"# credential: a project MCP token in " + "the Waves config folder (mode 600), not in the project",
	}
	return settings, mcpFile, lines, nil
}

// isWavesHook recognizes the entry this binary installed.
func isWavesHook(e any) bool {
	m, _ := e.(map[string]any)
	if m == nil || m["matcher"] != hookMatcher {
		return false
	}
	hs, _ := m["hooks"].([]any)
	for _, h := range hs {
		hm, _ := h.(map[string]any)
		if c, _ := hm["command"].(string); strings.Contains(c, " hook --policy ") {
			return true
		}
	}
	return false
}

func planHash(a, b []byte) string {
	h := sha256.New()
	h.Write(a)
	h.Write([]byte{0})
	h.Write(b)
	return "ph_" + hex.EncodeToString(h.Sum(nil))[:24]
}

// ConnectPlan is a dry run: the exact edits connecting would make.
func (s *Service) ConnectPlan(ctx context.Context, projectID string) (contract.ConnectPlan, error) {
	root, err := s.projectPath(ctx, s.db.R, projectID)
	if err != nil {
		return contract.ConnectPlan{}, err
	}
	policy := s.projectPolicy(ctx, projectID)
	if policy == "off" {
		policy = "audit"
	}
	a, b, lines, err := plannedEdits(root, projectID, policy)
	if err != nil {
		return contract.ConnectPlan{}, errValidation(err.Error(), "")
	}
	return contract.ConnectPlan{PlanHash: planHash(a, b), File: filesOf(root).settings, Lines: lines, Applied: s.connected(ctx, projectID)}, nil
}

func (s *Service) projectPolicy(ctx context.Context, projectID string) string {
	var p string
	s.db.R.QueryRowContext(ctx, `SELECT guard_policy FROM project_settings WHERE project_id = ?`, projectID).Scan(&p)
	return p
}

func (s *Service) connected(ctx context.Context, projectID string) bool {
	var c int
	s.db.R.QueryRowContext(ctx, `SELECT connected FROM adapter_configs a JOIN harnesses h ON h.id = a.harness_id WHERE a.project_id = ? AND h.kind = 'claude-code'`, projectID).Scan(&c)
	return c == 1
}

type changeRecord struct {
	SettingsCreated bool   `json:"settings_created"`
	MCPCreated      bool   `json:"mcp_created"`
	AddedEnabled    bool   `json:"added_enabled"`
	HadServer       bool   `json:"had_server"`
	PrevServer      any    `json:"prev_server,omitempty"`
	Policy          string `json:"policy"`
}

// Connect applies the reviewed plan. It needs a human elevation, refuses a
// plan whose edits changed since review, and records exactly what it changed.
func (s *Service) Connect(ctx context.Context, p Principal, elevation, projectID, hash string) (contract.ConnectPlan, error) {
	plan, err := s.ConnectPlan(ctx, projectID)
	if err != nil {
		return plan, err
	}
	if plan.PlanHash != hash {
		return plan, fail(http.StatusConflict, "stale_subject", "The edits changed after you reviewed them.", "Reload the plan and review it again.")
	}
	root, _ := s.projectPath(ctx, s.db.R, projectID)
	policy := s.projectPolicy(ctx, projectID)
	if policy == "off" {
		policy = "audit"
	}
	f := filesOf(root)
	sm, sExists, err := readJSONObject(f.settings)
	if err != nil {
		return plan, errValidation(err.Error(), "")
	}
	mm, mExists, _ := readJSONObject(f.mcp)
	rec := changeRecord{SettingsCreated: !sExists, MCPCreated: !mExists, Policy: policy}
	en, _ := sm["enabledMcpjsonServers"].([]any)
	rec.AddedEnabled = true
	for _, x := range en {
		if x == "waves" {
			rec.AddedEnabled = false
		}
	}
	if srv, _ := mm["mcpServers"].(map[string]any); srv != nil {
		if prev, ok := srv["waves"]; ok {
			rec.HadServer, rec.PrevServer = true, prev
		}
	}
	a, b, _, err := plannedEdits(root, projectID, policy)
	if err != nil {
		return plan, errValidation(err.Error(), "")
	}
	tok := NewSecret(PrefixMCP)
	err = s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		hid, err := s.claudeHarnessID(ctx, t)
		if err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE credentials SET revoked_at = ? WHERE project_id = ? AND revoked_at IS NULL`, s.ts(), projectID); err != nil {
			return wrap(err, "revoke old credential")
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO credentials(id, scope, project_id, token_hash, created_at) VALUES (?, 'mcp_project', ?, ?, ?)`, NewID("crd"), projectID, HashSecret(tok), s.ts()); err != nil {
			return wrap(err, "create project credential")
		}
		rb, _ := json.Marshal(rec)
		if _, err := t.ExecContext(ctx, `INSERT INTO adapter_configs(project_id, harness_id, connected, changes_json, connected_at) VALUES (?,?,1,?,?)
			ON CONFLICT(project_id, harness_id) DO UPDATE SET connected = 1, changes_json = excluded.changes_json, connected_at = excluded.connected_at`, projectID, hid, string(rb), s.ts()); err != nil {
			return wrap(err, "record connection")
		}
		if _, err := t.ExecContext(ctx, `UPDATE project_settings SET guard_policy = ?, updated_at = ? WHERE project_id = ?`, policy, s.ts(), projectID); err != nil {
			return wrap(err, "set policy")
		}
		// Files are written inside the transaction so a failed write rolls the records back.
		if err := s.writeMCPToken(projectID, tok); err != nil {
			return err
		}
		if err := writeProjectFile(f.settings, a); err != nil {
			return err
		}
		if err := writeProjectFile(f.mcp, b); err != nil {
			return err
		}
		t.publish("harness.changed", map[string]string{"harness_id": hid, "project_id": projectID})
		t.publish("project.changed", map[string]string{"project_id": projectID})
		return t.audit(ctx, store.AuditRecord{Event: "agent.connected", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: projectID, Detail: map[string]any{"files": []string{f.settings, f.mcp}, "policy": policy}})
	})
	if err != nil {
		return plan, err
	}
	plan.Applied = true
	return plan, nil
}

func writeProjectFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return wrap(err, "create %s", filepath.Dir(path))
	}
	return wrap(os.WriteFile(path, b, 0o644), "write %s", path)
}

func (s *Service) mcpTokenPath(projectID string) string {
	return filepath.Join(s.dirs.Config, "mcp", projectID+".token")
}

func (s *Service) writeMCPToken(projectID, tok string) error {
	if err := os.MkdirAll(filepath.Join(s.dirs.Config, "mcp"), 0o700); err != nil {
		return wrap(err, "create token dir")
	}
	return config.WriteSecret(s.mcpTokenPath(projectID), []byte(tok+"\n"))
}

// Disconnect reverts exactly the recorded edits and revokes the credential.
func (s *Service) Disconnect(ctx context.Context, p Principal, elevation, projectID string) (contract.ConnectPlan, error) {
	root, err := s.projectPath(ctx, s.db.R, projectID)
	if err != nil {
		return contract.ConnectPlan{}, err
	}
	var raw string
	err = s.db.R.QueryRowContext(ctx, `SELECT a.changes_json FROM adapter_configs a JOIN harnesses h ON h.id = a.harness_id WHERE a.project_id = ? AND h.kind = 'claude-code' AND a.connected = 1`, projectID).Scan(&raw)
	if err != nil {
		return contract.ConnectPlan{}, errInvalidState("Claude Code is not connected to this project.", "")
	}
	var rec changeRecord
	json.Unmarshal([]byte(raw), &rec)
	f := filesOf(root)
	lines := []string{"# " + f.settings, `- the Waves PreToolUse hook`, "# " + f.mcp, `- "mcpServers.waves"`}
	err = s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		if _, err := t.ExecContext(ctx, `UPDATE adapter_configs SET connected = 0, changes_json = '[]' WHERE project_id = ?`, projectID); err != nil {
			return wrap(err, "record disconnection")
		}
		if _, err := t.ExecContext(ctx, `UPDATE credentials SET revoked_at = ? WHERE project_id = ? AND revoked_at IS NULL`, s.ts(), projectID); err != nil {
			return wrap(err, "revoke credential")
		}
		if _, err := t.ExecContext(ctx, `UPDATE project_settings SET guard_policy = 'off', updated_at = ? WHERE project_id = ?`, s.ts(), projectID); err != nil {
			return wrap(err, "set policy")
		}
		if err := revertSettings(f.settings, rec); err != nil {
			return err
		}
		if err := revertMCP(f.mcp, rec); err != nil {
			return err
		}
		os.Remove(s.mcpTokenPath(projectID))
		t.publish("harness.changed", map[string]string{"harness_id": "claude-code", "project_id": projectID})
		t.publish("project.changed", map[string]string{"project_id": projectID})
		return t.audit(ctx, store.AuditRecord{Event: "agent.disconnected", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: projectID})
	})
	return contract.ConnectPlan{PlanHash: "ph_removed", File: f.settings, Lines: lines, Applied: true}, err
}

func revertSettings(path string, rec changeRecord) error {
	m, exists, err := readJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if hooks, _ := m["hooks"].(map[string]any); hooks != nil {
		pre, _ := hooks["PreToolUse"].([]any)
		var kept []any
		for _, e := range pre {
			if !isWavesHook(e) {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(hooks, "PreToolUse")
		} else {
			hooks["PreToolUse"] = kept
		}
		if len(hooks) == 0 {
			delete(m, "hooks")
		}
	}
	if rec.AddedEnabled {
		en, _ := m["enabledMcpjsonServers"].([]any)
		var kept []any
		for _, x := range en {
			if x != "waves" {
				kept = append(kept, x)
			}
		}
		if len(kept) == 0 {
			delete(m, "enabledMcpjsonServers")
		} else {
			m["enabledMcpjsonServers"] = kept
		}
	}
	if len(m) == 0 && rec.SettingsCreated {
		return wrap(os.Remove(path), "remove %s", path)
	}
	return writeProjectFile(path, encodeJSON(m))
}

func revertMCP(path string, rec changeRecord) error {
	m, exists, err := readJSONObject(path)
	if err != nil || !exists {
		return err
	}
	if srv, _ := m["mcpServers"].(map[string]any); srv != nil {
		if rec.HadServer {
			srv["waves"] = rec.PrevServer
		} else {
			delete(srv, "waves")
		}
		if len(srv) == 0 {
			delete(m, "mcpServers")
		}
	}
	if len(m) == 0 && rec.MCPCreated {
		return wrap(os.Remove(path), "remove %s", path)
	}
	return writeProjectFile(path, encodeJSON(m))
}

// SetProjectGuard changes the guard policy or the unmanaged-session rule.
// It needs a human elevation; an installed hook is rewritten to the new policy.
func (s *Service) SetProjectGuard(ctx context.Context, p Principal, elevation, projectID, policy, unmanaged string) error {
	root, err := s.projectPath(ctx, s.db.R, projectID)
	if err != nil {
		return err
	}
	if policy != "" && policy != "off" && policy != "audit" && policy != "enforce" {
		return errValidation("Unknown guard policy.", "Use off, audit or enforce.")
	}
	if unmanaged != "" && unmanaged != "allow" && unmanaged != "deny" {
		return errValidation("unmanaged_sessions must be allow or deny.", "")
	}
	conn := s.connected(ctx, projectID)
	if policy != "" && policy != "off" && !conn {
		return errInvalidState("The guard hook is not installed in this project.", "Connect Claude Code first.")
	}
	return s.tx(ctx, func(t *Tx) error {
		if err := s.RequireElevation(ctx, t, p, elevation); err != nil {
			return err
		}
		old := s.projectPolicy(ctx, projectID)
		if policy != "" {
			if _, err := t.ExecContext(ctx, `UPDATE project_settings SET guard_policy = ?, updated_at = ? WHERE project_id = ?`, policy, s.ts(), projectID); err != nil {
				return wrap(err, "set policy")
			}
			if conn {
				a, _, _, err := plannedEdits(root, projectID, policy)
				if err != nil {
					return errValidation(err.Error(), "")
				}
				if err := writeProjectFile(filesOf(root).settings, a); err != nil {
					return err
				}
			}
		}
		if unmanaged != "" {
			if _, err := t.ExecContext(ctx, `UPDATE project_settings SET unmanaged_sessions = ?, updated_at = ? WHERE project_id = ?`, unmanaged, s.ts(), projectID); err != nil {
				return wrap(err, "set unmanaged rule")
			}
		}
		t.publish("harness.changed", map[string]string{"harness_id": "claude-code", "project_id": projectID})
		t.publish("project.changed", map[string]string{"project_id": projectID})
		return t.audit(ctx, store.AuditRecord{Event: "guard.policy_changed", ActorScope: "human", ActorRef: p.ActorRef(), ProjectID: projectID,
			Detail: map[string]any{"from": old, "to": policy, "unmanaged_sessions": unmanaged}})
	})
}

// GuardInfo is the guard panel of a project.
func (s *Service) GuardInfo(ctx context.Context, projectID string) (contract.GuardInfo, error) {
	if err := s.mustProject(ctx, s.db.R, projectID); err != nil {
		return contract.GuardInfo{}, err
	}
	g := contract.GuardInfo{NotDefended: NotDefended}
	s.db.R.QueryRowContext(ctx, `SELECT guard_policy, unmanaged_sessions FROM project_settings WHERE project_id = ?`, projectID).Scan(&g.Policy, &g.UnmanagedSessions)
	// Installed means the hook is on disk as Waves wrote it, not only that a connection was recorded.
	if s.connected(ctx, projectID) {
		if root, err := s.projectPath(ctx, s.db.R, projectID); err != nil {
			g.Drift = "the project folder cannot be read"
		} else {
			g.Drift = hookDrift(root, g.Policy)
		}
		g.Installed = g.Drift == ""
	}
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM guard_decisions WHERE project_id = ? AND would_deny = 1 AND ts > ?`, projectID, store.FormatTime(s.now().Add(-24*time.Hour))).Scan(&g.WouldDeny24h)
	return g, nil
}

// GuardDecisions lists recent decisions of a project, newest first.
func (s *Service) GuardDecisions(ctx context.Context, projectID string, limit int) (contract.GuardDecisionList, error) {
	out := contract.GuardDecisionList{Items: []contract.GuardDecision{}}
	if err := s.mustProject(ctx, s.db.R, projectID); err != nil {
		return out, err
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT ts, tool_name, COALESCE(target_path,''), decision, would_deny, reason FROM guard_decisions WHERE project_id = ? ORDER BY id DESC LIMIT ?`, projectID, limit)
	if err != nil {
		return out, wrap(err, "list guard decisions")
	}
	defer rows.Close()
	for rows.Next() {
		var d contract.GuardDecision
		var at string
		var wd int
		rows.Scan(&at, &d.Tool, &d.Target, &d.Decision, &wd, &d.Reason)
		d.At, d.WouldDeny = store.ParseTime(at), wd == 1
		out.Items = append(out.Items, d)
	}
	return out, rows.Err()
}

// Harnesses lists Claude Code with its observed capabilities.
func (s *Service) Harnesses(ctx context.Context) (contract.HarnessList, error) {
	h := contract.Harness{ID: "claude-code", Name: "Claude Code", Version: s.ClaudeCodeVersion(ctx), ConnectedProjects: []string{}, Capabilities: claudeCapabilities}
	_, err := exec.LookPath("claude")
	h.Installed = err == nil || h.Version != ""
	rows, err := s.db.R.QueryContext(ctx, `SELECT p.name FROM adapter_configs a JOIN projects p ON p.id = a.project_id WHERE a.connected = 1 AND p.removed_at IS NULL ORDER BY p.name`)
	if err != nil {
		return contract.HarnessList{}, wrap(err, "list connections")
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		rows.Scan(&n)
		h.ConnectedProjects = append(h.ConnectedProjects, n)
	}
	return contract.HarnessList{Items: []contract.Harness{h}}, rows.Err()
}

// harnessBrief is the connected harness of a project, if any.
func (s *Service) harnessBrief(ctx context.Context, projectID string) (*contract.HarnessBrief, error) {
	if !s.connected(ctx, projectID) {
		return nil, errors.New("not connected")
	}
	g, err := s.GuardInfo(ctx, projectID)
	if err != nil {
		return nil, err
	}
	var cred int
	s.db.R.QueryRowContext(ctx, `SELECT count(*) FROM credentials WHERE project_id = ? AND revoked_at IS NULL`, projectID).Scan(&cred)
	return &contract.HarnessBrief{Name: "Claude Code", Version: s.ClaudeCodeVersion(ctx), Connected: true, GuardPolicy: g.Policy, WouldDeny24h: g.WouldDeny24h, CredentialActive: cred > 0}, nil
}

// detectClaude records the installed Claude Code version once at start.
func (s *Service) detectClaude(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(c, "claude", "--version").Output()
	if err != nil {
		return
	}
	v := strings.Fields(string(out))
	if len(v) == 0 {
		return
	}
	s.tx(ctx, func(t *Tx) error {
		id, err := s.claudeHarnessID(ctx, t)
		if err != nil {
			return err
		}
		_, err = t.ExecContext(ctx, `UPDATE harnesses SET detected_version = ?, updated_at = ? WHERE id = ?`, v[0], s.ts(), id)
		return err
	})
}
