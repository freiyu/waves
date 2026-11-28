// Package mcp is the Waves MCP server (Streamable HTTP at /mcp) and the
// stdio bridge `waves mcp` that Claude Code starts. It exposes planning and
// task tools only: no tool approves, rejects, or changes settings.
package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"waves/internal/core"
)

// MaxBody is the request limit on /mcp (proposals may be up to 1 MiB of JSON inside a request).
const MaxBody = 4 << 20

// sessionLimit is how many session bindings are kept before the closed ones are dropped. A variable only so tests can lower
// it; there is no flag or environment variable for it.
var sessionLimit = 4096

// Tools are the tool names, in the order they are listed.
var Tools = []string{
	"waves_get_planning_request", "waves_submit_workflow_proposal", "waves_list_available_tasks", "waves_claim_task",
	"waves_get_task_context", "waves_report_progress", "waves_report_blocker", "waves_request_approval",
	"waves_submit_result", "waves_heartbeat", "waves_release_task",
}

type server struct {
	core *core.Service
	srv  *sdk.Server
	// sessions binds each MCP session to the credential that opened it. pruneAt is the size at which closed ones are next
	// looked for, so that many live sessions do not make every new one scan them all.
	mu       sync.Mutex
	sessions map[string]string
	pruneAt  int
}

// Handler returns the /mcp handler. Every request must carry a project MCP
// credential; other credentials are refused and audited.
func Handler(c *core.Service) http.Handler {
	s := &server{core: c, sessions: map[string]string{}}
	srv := sdk.NewServer(&sdk.Implementation{Name: "waves", Version: c.Version()}, &sdk.ServerOptions{
		Instructions: "Waves serves the human-approved plan. Claim a task, follow its context, report progress, and submit the result; your lease lasts while this session stays connected. Nothing here approves anything.",
	})
	s.srv = srv
	s.register(srv)
	// A call to a tool Waves does not offer (an approval tool, say) is refused by the SDK; record who tried.
	srv.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if ct, ok := req.(*sdk.CallToolRequest); ok && ct.Params != nil && !slices.Contains(Tools, ct.Params.Name) {
				var h http.Header
				if ct.Extra != nil {
					h = ct.Extra.Header
				}
				c.AuditRejected(ctx, s.principal(ctx, h, "mcp"), "mcp unknown tool "+truncate(ct.Params.Name, 64))
			}
			return next(ctx, method, req)
		}
	})
	h := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return srv }, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
		p := s.principal(r.Context(), r.Header, r.RemoteAddr)
		if p.Kind != core.KindMCP {
			if p.Kind != core.KindNone {
				c.AuditRejected(r.Context(), p, "mcp")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"error":{"code":"unauthenticated","message":"MCP needs the project credential.","next_step":"Run waves agent connect claude-code."}}`))
			return
		}
		// A session answers only to the credential that opened it, for every method: the SDK on its own would let any valid
		// credential that names it send to it, take its stream, or close it. The answer is the SDK's own for an unknown id.
		if sid := r.Header.Get("Mcp-Session-Id"); sid != "" && s.foreign(sid, p.CredID) {
			c.AuditRejected(r.Context(), p, "mcp session of another credential")
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}
		// The stream the bridge keeps open (GET) tells Waves the session is connected for as long as it lasts, even while one tool
		// call runs for minutes and nothing else is said. The session must belong to this credential.
		// Only a session the SDK still holds counts: for a closed or unknown one the SDK answers 404, and a stream that is refused
		// must not hold a lease.
		if sid := r.Header.Get("Mcp-Session-Id"); r.Method == http.MethodGet && sid != "" && s.live(sid) && s.owns(sid, p.CredID) {
			// The server's write limit would cut a stream that is silent for a minute and then carries a notification.
			if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
				slog.Warn("mcp stream: the write limit could not be lifted", "err", err)
			}
			if err := c.StreamOpen(r.Context(), p, sid); err != nil {
				slog.Error("mcp stream open", "err", err)
			} else {
				defer func() {
					ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
					defer cancel()
					if err := c.StreamClose(ctx, p, sid); err != nil {
						slog.Error("mcp stream close", "err", err)
					}
				}()
			}
		}
		// The session that an initialize request opens belongs to the credential that opened it: a stream may only be
		// attached by that credential, so no other credential can claim an unbound session id first. It is bound before the
		// first byte of the answer leaves, because the client may open its stream while this request is still being served.
		if r.Method == http.MethodPost && r.Header.Get("Mcp-Session-Id") == "" {
			b := &binder{ResponseWriter: w, server: s, cred: p.CredID}
			defer b.bindSession() // an answer without a body: its headers leave after the handler returns
			w = b
		}
		h.ServeHTTP(w, r)
	})
}

// binder binds the session named in the response headers to the credential just before anything is sent.
type binder struct {
	http.ResponseWriter
	server *server
	cred   string
}

func (b *binder) bindSession() {
	if sid := b.Header().Get("Mcp-Session-Id"); sid != "" {
		b.server.bind(sid, b.cred)
	}
}

func (b *binder) WriteHeader(status int) {
	b.bindSession()
	b.ResponseWriter.WriteHeader(status)
}

func (b *binder) Write(p []byte) (int, error) {
	b.bindSession()
	return b.ResponseWriter.Write(p)
}

func (b *binder) Flush() { _ = b.FlushError() }

// FlushError is what http.ResponseController calls: a flush alone also sends the headers.
func (b *binder) FlushError() error {
	b.bindSession()
	return http.NewResponseController(b.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the deadlines of the real writer.
func (b *binder) Unwrap() http.ResponseWriter { return b.ResponseWriter }

// bind ties an MCP session to the credential that uses it and reports whether it is this one's. Session ids are assigned by
// the service and random, so a credential that does not hold the session cannot name it.
func (s *server) bind(sid, cred string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, known := s.sessions[sid]
	if known {
		return prev == cred
	}
	if len(s.sessions) >= max(sessionLimit, s.pruneAt) {
		s.dropClosed()
		s.pruneAt = 2 * len(s.sessions)
	}
	s.sessions[sid] = cred
	return true
}

// dropClosed forgets the bindings of sessions the SDK no longer holds. A live session is never forgotten: without its
// binding another credential could claim it. The caller holds s.mu (the SDK never calls in while it holds its own lock).
func (s *server) dropClosed() {
	open := map[string]bool{}
	for ss := range s.srv.Sessions() {
		open[ss.ID()] = true
	}
	for sid := range s.sessions {
		if !open[sid] {
			delete(s.sessions, sid)
		}
	}
}

// owns reports whether the session was opened by this credential.
func (s *server) owns(sid, cred string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[sid] == cred
}

// foreign reports whether the session is bound to a different credential. It binds nothing.
func (s *server) foreign(sid, cred string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.sessions[sid]
	return ok && prev != cred
}

// live reports whether the SDK holds a session with this id.
func (s *server) live(sid string) bool {
	for ss := range s.srv.Sessions() {
		if ss.ID() == sid {
			return true
		}
	}
	return false
}

func (s *server) principal(ctx context.Context, h http.Header, src string) core.Principal {
	tok, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")
	if !ok {
		return core.Principal{Kind: core.KindNone, Source: src}
	}
	p, _ := s.core.Bearer(ctx, strings.TrimSpace(tok), src)
	return p
}

// caller authenticates one tool call and checks that the MCP session stays
// with the credential that started it.
func (s *server) caller(ctx context.Context, req *sdk.CallToolRequest) (core.Principal, string, error) {
	var h http.Header
	if req.Extra != nil {
		h = req.Extra.Header
	}
	p := s.principal(ctx, h, "mcp")
	if p.Kind != core.KindMCP {
		return p, "", &core.Error{Code: "unauthenticated", Message: "MCP needs the project credential."}
	}
	sid := ""
	if req.Session != nil {
		sid = req.Session.ID()
	}
	if !s.bind(sid, p.CredID) {
		return p, "", &core.Error{Code: "lease_invalid", Message: "This MCP session belongs to another credential."}
	}
	return p, sid, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func toolUseID(req *sdk.CallToolRequest) string {
	if req.Params == nil {
		return ""
	}
	v, _ := req.Params.GetMeta()["claudecode/toolUseId"].(string)
	return v
}

// result turns a value or an error into a tool result. Errors carry the
// stable code and never name other tasks.
func result(v any, err error) (*sdk.CallToolResult, any, error) {
	if err != nil {
		e := core.AsError(err)
		var body map[string]string
		if e != nil {
			body = map[string]string{"error": e.Code, "message": e.Message, "next_step": e.NextStep}
		} else {
			// The agent gets an id, the service log gets the cause.
			id := core.NewID("req")
			slog.Error("mcp tool failed", "request_id", id, "err", err)
			body = map[string]string{"error": "internal", "message": "Waves hit an internal error (request " + id + ").", "next_step": "Stop and report a blocker; the human sees details in the service log."}
		}
		b, _ := json.Marshal(body)
		return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
	}
	b, _ := json.Marshal(v)
	return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: string(b)}}}, nil, nil
}

type planningIn struct {
	PlanningRequestID string `json:"planning_request_id,omitempty" jsonschema:"the planning request id; omit for the open one"`
}
type proposalIn struct {
	PlanningRequestID string         `json:"planning_request_id" jsonschema:"the planning request this proposal answers"`
	Proposal          map[string]any `json:"proposal" jsonschema:"the workflow proposal document (schema_version 1)"`
}
type emptyIn struct{}
type claimIn struct {
	TaskID      string `json:"task_id" jsonschema:"a task id from waves_list_available_tasks"`
	SessionHint string `json:"session_hint,omitempty" jsonschema:"optional, ignored for authorization"`
}
type leaseIn struct {
	LeaseToken string `json:"lease_token,omitempty" jsonschema:"the lease token from waves_claim_task; may be omitted when this session holds one lease"`
}
type progressIn struct {
	LeaseToken string `json:"lease_token,omitempty"`
	Message    string `json:"message" jsonschema:"what you did or found"`
	Percent    *int   `json:"percent,omitempty"`
}
type blockerIn struct {
	LeaseToken string `json:"lease_token,omitempty"`
	Reason     string `json:"reason" jsonschema:"why you cannot continue"`
}
type approvalIn struct {
	LeaseToken string `json:"lease_token,omitempty"`
	Action     string `json:"action" jsonschema:"the risky action you want to take"`
	Reason     string `json:"reason"`
	Impact     string `json:"impact" jsonschema:"what it changes and what could go wrong"`
}
type resultIn struct {
	LeaseToken string   `json:"lease_token,omitempty"`
	Summary    string   `json:"summary" jsonschema:"what was done and how it meets the acceptance criteria"`
	Status     string   `json:"status" jsonschema:"completed or failed"`
	Artifacts  []string `json:"artifacts,omitempty"`
}
type releaseIn struct {
	LeaseToken string `json:"lease_token,omitempty"`
	Reason     string `json:"reason"`
}

func (s *server) register(srv *sdk.Server) {
	ro := &sdk.ToolAnnotations{ReadOnlyHint: true}
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_get_planning_request", Description: "Read the open planning request: requirements, proposal schema, rules, and errors of the last invalid proposal.", Annotations: ro},
		func(ctx context.Context, req *sdk.CallToolRequest, in planningIn) (*sdk.CallToolResult, any, error) {
			p, _, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(s.core.PlanningRequestBody(ctx, p.ProjectID, in.PlanningRequestID))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_submit_workflow_proposal", Description: "Submit a workflow proposal. It is stored unchanged; a valid one becomes a draft that a human must approve."},
		func(ctx context.Context, req *sdk.CallToolRequest, in proposalIn) (*sdk.CallToolResult, any, error) {
			p, _, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			// Store the proposal exactly as it came over the wire, not re-encoded.
			var raw struct {
				Proposal json.RawMessage `json:"proposal"`
			}
			if req.Params != nil {
				json.Unmarshal(req.Params.Arguments, &raw)
			}
			id, valid, errs, err := s.core.SubmitProposal(ctx, p.ProjectID, in.PlanningRequestID, raw.Proposal, p.ActorRef())
			return result(map[string]any{"proposal_id": id, "valid": valid, "errors": errs}, err)
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_list_available_tasks", Description: "List Ready tasks of approved, open phases of this project.", Annotations: ro},
		func(ctx context.Context, req *sdk.CallToolRequest, _ emptyIn) (*sdk.CallToolResult, any, error) {
			p, _, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			l, err := s.core.AvailableTasks(ctx, p)
			return result(map[string]any{"tasks": l}, err)
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_claim_task", Description: "Claim one Ready task (task taken). Returns a lease bound to this MCP session. The lease lasts while this session stays connected; no heartbeat is needed."},
		func(ctx context.Context, req *sdk.CallToolRequest, in claimIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(s.core.ClaimTask(ctx, p, sid, in.TaskID, toolUseID(req)))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_get_task_context", Description: "Get the context package of your leased task: task, acceptance criteria, phase objective, requirements, rules.", Annotations: ro},
		func(ctx context.Context, req *sdk.CallToolRequest, in leaseIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(s.core.TaskContext(ctx, p, sid, in.LeaseToken))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_report_progress", Description: "Report progress on your leased task."},
		func(ctx context.Context, req *sdk.CallToolRequest, in progressIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(map[string]bool{"ok": true}, s.core.ReportProgress(ctx, p, sid, in.LeaseToken, in.Message, in.Percent))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_report_blocker", Description: "Report that you cannot continue. The task waits for the human."},
		func(ctx context.Context, req *sdk.CallToolRequest, in blockerIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(map[string]bool{"ok": true}, s.core.ReportBlocker(ctx, p, sid, in.LeaseToken, in.Reason))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_request_approval", Description: "Ask the human to allow a risky action. Wait; the decision arrives in the next waves_heartbeat. You cannot approve it yourself."},
		func(ctx context.Context, req *sdk.CallToolRequest, in approvalIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			id, err := s.core.RequestRiskyApproval(ctx, p, sid, in.LeaseToken, in.Action, in.Reason, in.Impact)
			return result(map[string]string{"approval_id": id, "state": "pending"}, err)
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_submit_result", Description: "Submit the result of your leased task (task done). The lease ends and the task is Done; the human reviews the results of the whole phase at its end."},
		func(ctx context.Context, req *sdk.CallToolRequest, in resultIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(map[string]bool{"ok": true}, s.core.SubmitResult(ctx, p, sid, in.LeaseToken, in.Summary, in.Status, in.Artifacts))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_heartbeat", Description: "Check your lease: whether it is still valid, the phase state, a cancel request, and a pending approval's decision. Not needed to keep the lease alive; use it while you wait for an approval decision."},
		func(ctx context.Context, req *sdk.CallToolRequest, in leaseIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			return result(s.core.Heartbeat(ctx, p, sid, in.LeaseToken))
		})
	sdk.AddTool(srv, &sdk.Tool{Name: "waves_release_task", Description: "Give the task back. It returns to Ready only if the workspace is unchanged since you claimed it; otherwise it is Blocked for the human."},
		func(ctx context.Context, req *sdk.CallToolRequest, in releaseIn) (*sdk.CallToolResult, any, error) {
			p, sid, err := s.caller(ctx, req)
			if err != nil {
				return result(nil, err)
			}
			to, err := s.core.ReleaseTask(ctx, p, sid, in.LeaseToken, in.Reason)
			return result(map[string]string{"task_state": to}, err)
		})
}
