package contract

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"
)

// Route is one HTTP operation. Req and Resp are example values used to
// reflect the schema; a nil Resp means 204 No Content.
type Route struct {
	Method      string
	Path        string // relative to /api/v1
	Scope       string // none, op, human
	Summary     string
	Req         any
	Resp        any
	Status      int    // default 200
	ContentType string // default application/json
}

type text string

// Routes is the route table the web client is generated from. The service serves
// exactly these routes, plus the ones the web UI does not call.
var Routes = []Route{
	{Method: "GET", Path: "/health", Scope: "none", Summary: "Liveness and version", Resp: HealthResponse{}},
	{Method: "GET", Path: "/status", Scope: "op", Summary: "Counts, audit chain status, needs-attention list", Resp: StatusResponse{}},
	{Method: "GET", Path: "/auth/link/{code}", Scope: "none", Summary: "Check a login link without consuming it", Req: LinkStatusRequest{}, Resp: LinkStatus{}},
	{Method: "POST", Path: "/auth/consume", Scope: "none", Summary: "Consume a login link and set the session cookie", Req: ConsumeLinkRequest{}, Resp: Session{}},
	{Method: "GET", Path: "/auth/session", Scope: "op", Summary: "Scope, CSRF token, elevation state, expiry", Resp: Session{}},
	{Method: "POST", Path: "/auth/logout", Scope: "op", Summary: "Revoke the session"},
	{Method: "POST", Path: "/auth/stepup", Scope: "op", Summary: "Passphrase step-up; returns an elevation", Req: StepUpRequest{}, Resp: StepUpResponse{}},
	{Method: "POST", Path: "/auth/stepup/revoke", Scope: "op", Summary: "Forget an elevation now (lock again)", Req: RevokeRequest{}},
	{Method: "GET", Path: "/audit", Scope: "op", Summary: "Page through audit records", Req: AuditListRequest{}, Resp: AuditList{}},
	{Method: "POST", Path: "/audit/acknowledge", Scope: "human", Summary: "Acknowledge a broken audit chain", Resp: AuditStatus{}},
	{Method: "POST", Path: "/backup", Scope: "op", Summary: "Online database backup", Resp: BackupResult{}},

	{Method: "GET", Path: "/projects", Scope: "op", Summary: "List projects", Req: ProjectListRequest{}, Resp: ProjectList{}},
	{Method: "POST", Path: "/projects", Scope: "op", Summary: "Register a project", Req: RegisterProjectRequest{}, Resp: Project{}, Status: 201},
	{Method: "GET", Path: "/projects/{id}", Scope: "op", Summary: "Project overview", Req: IDPath{}, Resp: ProjectDetail{}},
	{Method: "POST", Path: "/projects/{id}/activate", Scope: "op", Summary: "Set the Active Context", Req: IDPath{}, Resp: Project{}},
	{Method: "GET", Path: "/settings", Scope: "op", Summary: "Global settings", Resp: Settings{}},
	{Method: "PATCH", Path: "/settings", Scope: "op", Summary: "Change settings; security keys need an elevation", Req: PatchSettingsRequest{}, Resp: Settings{}},

	{Method: "GET", Path: "/projects/{id}/requirements", Scope: "op", Summary: "List requirement sources", Req: IDPath{}, Resp: RequirementList{}},
	{Method: "POST", Path: "/projects/{id}/requirements", Scope: "op", Summary: "Add a requirement source", Req: AddRequirementRequest{}, Resp: Requirement{}, Status: 201},
	{Method: "POST", Path: "/projects/{id}/planning-requests", Scope: "op", Summary: "Create a planning request for a harness", Req: IDPath{}, Resp: PlanningRequest{}, Status: 201},
	{Method: "GET", Path: "/projects/{id}/workflow", Scope: "op", Summary: "Workflow with its versions", Req: IDPath{}, Resp: WorkflowInfo{}},
	{Method: "GET", Path: "/workflow-versions/{id}", Scope: "op", Summary: "Phases, tasks, validators with exact commands", Req: IDPath{}, Resp: WorkflowVersion{}},
	{Method: "POST", Path: "/workflow-versions/{id}/versions", Scope: "op", Summary: "New draft version", Req: IDPath{}, Resp: WorkflowVersionSummary{}, Status: 201},
	{Method: "POST", Path: "/workflow-versions/{id}/submit", Scope: "op", Summary: "Submit a draft for approval", Req: IDPath{}, Resp: SubmitResponse{}},
	{Method: "GET", Path: "/phases/{id}/review", Scope: "op", Summary: "Phase review package", Req: IDPath{}, Resp: PhaseReview{}},
	{Method: "POST", Path: "/phases/{id}/reopen", Scope: "human", Summary: "Send finished tasks of a phase whose review was rejected back to work (needs a passphrase step-up; a window is enough)", Req: ReopenTasksRequest{}},
	{Method: "POST", Path: "/phases/{id}/validation-runs/{vid}/confirm", Scope: "human", Summary: "Confirm one checklist item (not an approval; needs a passphrase step-up, a window is enough)", Req: ConfirmValidationRequest{}, Resp: ValidatorResult{}},

	{Method: "GET", Path: "/approvals", Scope: "op", Summary: "Approval inbox", Req: ApprovalListRequest{}, Resp: ApprovalList{}},
	{Method: "GET", Path: "/approvals/{id}", Scope: "op", Summary: "Approval detail", Req: IDPath{}, Resp: ApprovalDetail{}},
	{Method: "POST", Path: "/approvals/{id}/decision", Scope: "human", Summary: "Approve (human elevation) or reject", Req: DecisionRequest{}, Resp: ApprovalDetail{}},

	{Method: "GET", Path: "/executions", Scope: "op", Summary: "Executions across projects", Req: ExecutionListRequest{}, Resp: ExecutionList{}},
	{Method: "GET", Path: "/executions/{id}", Scope: "op", Summary: "Execution detail", Req: IDPath{}, Resp: ExecutionDetail{}},
	{Method: "GET", Path: "/executions/{id}/log", Scope: "op", Summary: "Log bytes from an offset", Req: LogRequest{}, Resp: text(""), ContentType: "text/plain"},
	{Method: "GET", Path: "/executions/{id}/diff", Scope: "op", Summary: "Stored unified diff", Req: IDPath{}, Resp: text(""), ContentType: "text/plain"},
	{Method: "GET", Path: "/executions/{id}/context", Scope: "op", Summary: "Delivered context package", Req: IDPath{}, Resp: ContextPackage{}},
	{Method: "POST", Path: "/executions/{id}/cancel", Scope: "op", Summary: "Interrupt: signal the process group", Req: IDPath{}, Resp: ExecutionDetail{}, Status: 202},
	{Method: "POST", Path: "/executions/{id}/resolve", Scope: "op", Summary: "Decide an interrupted or disconnected execution", Req: ResolveRequest{}, Resp: ExecutionDetail{}},
	{Method: "POST", Path: "/executions/{id}/orphans/stop", Scope: "op", Summary: "Stop orphaned tool processes", Req: IDPath{}, Resp: ExecutionDetail{}},

	{Method: "GET", Path: "/harnesses", Scope: "op", Summary: "Harnesses and capabilities", Resp: HarnessList{}},
	{Method: "GET", Path: "/projects/{id}/guard", Scope: "op", Summary: "Guard policy and the not-defended statement", Req: IDPath{}, Resp: GuardInfo{}},
	{Method: "GET", Path: "/projects/{id}/guard/decisions", Scope: "op", Summary: "Recent guard decisions", Req: IDPath{}, Resp: GuardDecisionList{}},
	{Method: "PATCH", Path: "/projects/{id}/settings", Scope: "human", Summary: "Guard policy and unmanaged-session rule", Req: PatchProjectSettingsRequest{}, Resp: GuardInfo{}},
	{Method: "POST", Path: "/projects/{id}/agent/disconnect", Scope: "human", Summary: "Revert exactly the connection edits", Req: DisconnectRequest{}, Resp: ConnectPlan{}},
	{Method: "GET", Path: "/projects/{id}/agent/connect-plan", Scope: "op", Summary: "Dry run: the exact edits connecting would make", Req: IDPath{}, Resp: ConnectPlan{}},
	{Method: "POST", Path: "/projects/{id}/agent/connect", Scope: "human", Summary: "Apply the reviewed connection plan", Req: ConnectRequest{}, Resp: ConnectPlan{}},

	{Method: "GET", Path: "/events", Scope: "op", Summary: "Server-sent events (one JSON payload per event name)", Resp: EventPayloads{}, ContentType: "text/event-stream"},
}

// OpenAPI returns the OpenAPI 3.1 document for Routes as indented JSON.
func OpenAPI() ([]byte, error) {
	r := openapi31.NewReflector()
	// Slices are always sent as [] and never null: the service builds them with make.
	r.DefaultOptions = append(r.DefaultOptions, jsonschema.InterceptNullability(func(p jsonschema.InterceptNullabilityParams) {
		if p.Type.Kind() == reflect.Slice && !p.OmitEmpty && p.Schema != nil {
			p.Schema.RemoveType(jsonschema.Null)
		}
	}))
	r.Spec.Info.WithTitle("Waves API").WithVersion("1")
	r.Spec.WithServers(openapi31.Server{URL: "/api/v1"})
	for _, rt := range Routes {
		oc, err := r.NewOperationContext(rt.Method, rt.Path)
		if err != nil {
			return nil, fmt.Errorf("operation %s %s: %w", rt.Method, rt.Path, err)
		}
		oc.SetSummary(rt.Summary)
		oc.SetDescription("Scope: " + rt.Scope)
		if rt.Req != nil {
			oc.AddReqStructure(rt.Req)
		}
		if rt.Resp == nil {
			oc.AddRespStructure(nil, openapi.WithHTTPStatus(http.StatusNoContent))
		} else {
			status := rt.Status
			if status == 0 {
				status = http.StatusOK
			}
			opts := []openapi.ContentOption{openapi.WithHTTPStatus(status)}
			if rt.ContentType != "" {
				opts = append(opts, openapi.WithContentType(rt.ContentType))
			}
			oc.AddRespStructure(rt.Resp, opts...)
		}
		oc.AddRespStructure(new(ErrorBody), openapi.WithHTTPStatus(http.StatusBadRequest))
		if err := r.AddOperation(oc); err != nil {
			return nil, fmt.Errorf("add %s %s: %w", rt.Method, rt.Path, err)
		}
	}
	b, err := json.MarshalIndent(r.Spec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	return append(b, '\n'), nil
}
