// Package contract holds the HTTP contract as Go types. It is the source of
// the OpenAPI document and, through it, of the web client's TypeScript types.
//
// Conventions: a field without omitempty is always present and carries
// required:"true" (the generator marks everything else optional); enums use
// enum:"a,b"; a pointer means "present but null is allowed".
package contract

import "time"

// ---- errors and paging ----

type ErrorBody struct {
	Error ErrorDetail `json:"error" required:"true"`
}

type ErrorDetail struct {
	Code         string `json:"code" required:"true"`
	Message      string `json:"message" required:"true"`
	NextStep     string `json:"next_step,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	AttemptsLeft *int   `json:"attempts_left,omitempty"`
	RetryAfterS  *int   `json:"retry_after_s,omitempty"`
	Record       *int   `json:"record,omitempty"`
}

// ListQuery is embedded in every list request.
type ListQuery struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"200"`
	Cursor string `query:"cursor"`
}

type IDPath struct {
	ID string `path:"id" required:"true"`
}

type Empty struct{}

// ---- system and auth ----

type HealthResponse struct {
	Version string `json:"version" required:"true"`
}

type LinkStatusRequest struct {
	Code string `path:"code" required:"true"`
}

type LinkStatus struct {
	State      string `json:"state" required:"true" enum:"valid,invalid"`
	ExpiresInS int    `json:"expires_in_s,omitempty"`
}

type ConsumeLinkRequest struct {
	Code string `json:"code" required:"true"`
}

type Elevation struct {
	Kind       string    `json:"kind" required:"true" enum:"window,single_use"`
	ExpiresAt  time.Time `json:"expires_at" required:"true"`
	ApprovalID string    `json:"approval_id,omitempty"`
}

type Session struct {
	Scope         string     `json:"scope" required:"true" enum:"operator,human"`
	CSRFToken     string     `json:"csrf_token" required:"true"`
	ExpiresAt     time.Time  `json:"expires_at" required:"true"`
	PassphraseSet bool       `json:"passphrase_set" required:"true"`
	Elevation     *Elevation `json:"elevation,omitempty"`
}

type StepUpRequest struct {
	Passphrase string `json:"passphrase" required:"true"`
	ApprovalID string `json:"approval_id,omitempty"`
}

type StepUpResponse struct {
	ElevationSecret string    `json:"elevation_secret" required:"true"`
	Kind            string    `json:"kind" required:"true" enum:"window,single_use"`
	ExpiresAt       time.Time `json:"expires_at" required:"true"`
	ApprovalID      string    `json:"approval_id,omitempty"`
}

type AuditStatus struct {
	State          string     `json:"state" required:"true" enum:"verified,broken"`
	Records        int        `json:"records" required:"true"`
	LastCheckAt    time.Time  `json:"last_check_at" required:"true"`
	LastCheckpoint *time.Time `json:"last_checkpoint_at,omitempty"`
	BrokenAtRecord int        `json:"broken_at_record,omitempty"`
}

type Counts struct {
	Running         int `json:"running" required:"true"`
	Queued          int `json:"queued" required:"true"`
	WaitingApproval int `json:"waiting_approval" required:"true"`
	Failed          int `json:"failed" required:"true"`
	Disconnected    int `json:"disconnected" required:"true"`
}

type AttentionItem struct {
	Kind        string    `json:"kind" required:"true" enum:"approval,execution_failed,execution_disconnected,task_blocked,audit_chain,stepup_failures,passphrase_reset,database_restored,bypass_attempt"`
	Label       string    `json:"label" required:"true"`
	Subject     string    `json:"subject" required:"true"`
	ProjectID   string    `json:"project_id,omitempty"`
	ProjectName string    `json:"project_name,omitempty"`
	Since       time.Time `json:"since" required:"true"`
	RefType     string    `json:"ref_type" required:"true" enum:"approval,execution,audit"`
	RefID       string    `json:"ref_id" required:"true"`
}

type NextStep struct {
	Text    string `json:"text" required:"true"`
	RefType string `json:"ref_type,omitempty" enum:"approval,execution,audit"`
	RefID   string `json:"ref_id,omitempty"`
}

type StatusResponse struct {
	ServerVersion     string          `json:"server_version" required:"true"`
	ClaudeCodeVersion string          `json:"claude_code_version,omitempty"`
	Counts            Counts          `json:"counts" required:"true"`
	Audit             AuditStatus     `json:"audit" required:"true"`
	NeedsAttention    []AttentionItem `json:"needs_attention" required:"true"`
	NextStep          *NextStep       `json:"next_step,omitempty"`
}

type AuditRecord struct {
	Seq        int       `json:"seq" required:"true"`
	At         time.Time `json:"at" required:"true"`
	Event      string    `json:"event" required:"true"`
	ActorScope string    `json:"actor_scope" required:"true" enum:"system,operator,human,guard,agent,local_offline"`
	Subject    string    `json:"subject" required:"true"`
	Detail     string    `json:"detail" required:"true"`
}

type AuditListRequest struct{ ListQuery }

type AuditList struct {
	Items      []AuditRecord `json:"items" required:"true"`
	NextCursor string        `json:"next_cursor,omitempty"`
	Status     AuditStatus   `json:"status" required:"true"`
}

// ---- projects ----

type PhaseRef struct {
	ID    string `json:"id" required:"true"`
	Code  string `json:"code" required:"true"`
	Title string `json:"title" required:"true"`
	State string `json:"state" required:"true" enum:"planned,awaiting_approval,approved,ready_for_execution,in_progress,blocked,validation,awaiting_phase_review,completed,revision_required,failed,cancelled"`
}

type Project struct {
	ID                string     `json:"id" required:"true"`
	Name              string     `json:"name" required:"true"`
	Path              string     `json:"path" required:"true"`
	IsGit             bool       `json:"is_git" required:"true"`
	Branch            string     `json:"branch,omitempty"`
	BaseBranch        string     `json:"base_branch,omitempty"`
	Active            bool       `json:"active" required:"true"`
	ActivePhase       *PhaseRef  `json:"active_phase,omitempty"`
	ProgressPct       int        `json:"progress_pct" required:"true"`
	RunningExecutions int        `json:"running_executions" required:"true"`
	FailedExecutions  int        `json:"failed_executions" required:"true"`
	PendingApprovals  int        `json:"pending_approvals" required:"true"`
	GuardPolicy       string     `json:"guard_policy" required:"true" enum:"off,audit,enforce"`
	LastActivityAt    *time.Time `json:"last_activity_at,omitempty"`
}

type ProjectListRequest struct{ ListQuery }

type ProjectList struct {
	Items      []Project `json:"items" required:"true"`
	NextCursor string    `json:"next_cursor,omitempty"`
}

type RegisterProjectRequest struct {
	Path string `json:"path" required:"true"`
	Name string `json:"name,omitempty"`
}

type TaskSummary struct {
	ID          string   `json:"id" required:"true"`
	Code        string   `json:"code" required:"true"`
	Title       string   `json:"title" required:"true"`
	State       string   `json:"state" required:"true" enum:"backlog,ready,in_progress,waiting_approval,in_review,blocked,done,cancelled"`
	StateDetail string   `json:"state_detail,omitempty"`
	Mode        string   `json:"mode" required:"true" enum:"code_change,read_only"`
	DependsOn   []string `json:"depends_on" required:"true"`
	ExecutionID string   `json:"execution_id,omitempty"`
}

type HarnessBrief struct {
	Name             string `json:"name" required:"true"`
	Version          string `json:"version,omitempty"`
	Connected        bool   `json:"connected" required:"true"`
	GuardPolicy      string `json:"guard_policy" required:"true" enum:"off,audit,enforce"`
	WouldDeny24h     int    `json:"would_deny_24h" required:"true"`
	CredentialActive bool   `json:"credential_active" required:"true"`
}

type PlanningRequest struct {
	ID        string    `json:"id" required:"true"`
	State     string    `json:"state" required:"true" enum:"waiting,fetched,submitted"`
	CreatedAt time.Time `json:"created_at" required:"true"`
}

type ProposalError struct {
	Code    string `json:"code" required:"true"`
	Path    string `json:"path" required:"true"`
	Message string `json:"message" required:"true"`
}

type Proposal struct {
	ID                string          `json:"id" required:"true"`
	Version           int             `json:"version" required:"true"`
	Valid             bool            `json:"valid" required:"true"`
	Errors            []ProposalError `json:"errors" required:"true"`
	CreatedAt         time.Time       `json:"created_at" required:"true"`
	WorkflowVersionID string          `json:"workflow_version_id,omitempty"`
}

type Planning struct {
	Request        *PlanningRequest `json:"request,omitempty"`
	LatestProposal *Proposal        `json:"latest_proposal,omitempty"`
}

type RequirementRef struct {
	Source string `json:"source" required:"true"`
	State  string `json:"state" required:"true" enum:"unchanged,changed"`
}

type ProjectDetail struct {
	Project
	Phases           []PhaseRef        `json:"phases" required:"true"`
	Tasks            []TaskSummary     `json:"tasks" required:"true"`
	TasksPhase       string            `json:"tasks_phase,omitempty"`
	Harness          *HarnessBrief     `json:"harness,omitempty"`
	PendingApprovals []ApprovalSummary `json:"pending_approval_items" required:"true"`
	Requirements     []RequirementRef  `json:"requirements" required:"true"`
	Planning         Planning          `json:"planning" required:"true"`
	NextStep         *NextStep         `json:"next_step,omitempty"`
}

// ---- requirements and workflow ----

type Requirement struct {
	ID           string    `json:"id" required:"true"`
	Source       string    `json:"source" required:"true"`
	Kind         string    `json:"kind" required:"true" enum:"file,text"`
	RecordedHash string    `json:"recorded_hash,omitempty"`
	HashNow      string    `json:"hash_now,omitempty"`
	AddedAt      time.Time `json:"added_at" required:"true"`
	State        string    `json:"state" required:"true" enum:"unchanged,changed"`
	UsedBy       string    `json:"used_by,omitempty"`
	VersionID    string    `json:"version_id,omitempty"`
}

type RequirementList struct {
	Items []Requirement `json:"items" required:"true"`
}

type AddRequirementRequest struct {
	ID   string `path:"id" required:"true"`
	Kind string `json:"kind" required:"true" enum:"file,text"`
	Path string `json:"path,omitempty"`
	Text string `json:"text,omitempty"`
}

type WorkflowVersionSummary struct {
	ID          string    `json:"id" required:"true"`
	Number      int       `json:"number" required:"true"`
	State       string    `json:"state" required:"true" enum:"draft,awaiting_approval,approved,rejected,superseded"`
	ContentHash string    `json:"content_hash" required:"true"`
	CreatedAt   time.Time `json:"created_at" required:"true"`
}

type WorkflowInfo struct {
	Versions  []WorkflowVersionSummary `json:"versions" required:"true"`
	CurrentID string                   `json:"current_id,omitempty"`
}

type WorkflowTask struct {
	Code       string   `json:"code" required:"true"`
	Title      string   `json:"title" required:"true"`
	Mode       string   `json:"mode" required:"true" enum:"code_change,read_only"`
	Acceptance []string `json:"acceptance" required:"true"`
}

type Validator struct {
	ID       string   `json:"id" required:"true"`
	Kind     string   `json:"kind" required:"true" enum:"command,checklist"`
	Name     string   `json:"name" required:"true"`
	Command  string   `json:"command,omitempty"`
	Items    []string `json:"items,omitempty"`
	TimeoutS int      `json:"timeout_s,omitempty"`
}

type WorkflowPhase struct {
	ID         string         `json:"id" required:"true"`
	Code       string         `json:"code" required:"true"`
	Title      string         `json:"title" required:"true"`
	Objective  string         `json:"objective" required:"true"`
	Tasks      []WorkflowTask `json:"tasks" required:"true"`
	Validators []Validator    `json:"validators" required:"true"`
}

type WorkflowVersion struct {
	WorkflowVersionSummary
	ProjectID   string          `json:"project_id" required:"true"`
	ProjectName string          `json:"project_name" required:"true"`
	ProposalID  string          `json:"proposal_id,omitempty"`
	Phases      []WorkflowPhase `json:"phases" required:"true"`
}

type SubmitResponse struct {
	ApprovalID string `json:"approval_id" required:"true"`
}

// ---- phases ----

type ValidatorChecklistItem struct {
	Text      string `json:"text" required:"true"`
	Confirmed bool   `json:"confirmed" required:"true"`
}

type ValidatorResult struct {
	ID        string                   `json:"id" required:"true"`
	Name      string                   `json:"name" required:"true"`
	Kind      string                   `json:"kind" required:"true" enum:"command,checklist"`
	State     string                   `json:"state" required:"true" enum:"passed,failed,waiting"`
	ExitCode  *int                     `json:"exit_code,omitempty"`
	DurationS *int                     `json:"duration_s,omitempty"`
	Items     []ValidatorChecklistItem `json:"items,omitempty"`
}

type ReviewTask struct {
	ID           string `json:"id" required:"true"`
	Code         string `json:"code" required:"true"`
	Title        string `json:"title" required:"true"`
	State        string `json:"state" required:"true" enum:"backlog,ready,in_progress,waiting_approval,in_review,blocked,done,cancelled"`
	StateDetail  string `json:"state_detail,omitempty"`
	DurationS    *int   `json:"duration_s,omitempty"`
	Attempts     int    `json:"attempts" required:"true"`
	FilesChanged int    `json:"files_changed" required:"true"`
}

type Finding struct {
	Kind   string `json:"kind" required:"true" enum:"unattributed,out_of_scope"`
	Path   string `json:"path" required:"true"`
	Detail string `json:"detail" required:"true"`
}

type DiffSummary struct {
	Files      int `json:"files" required:"true"`
	Added      int `json:"added" required:"true"`
	Removed    int `json:"removed" required:"true"`
	Executions int `json:"executions" required:"true"`
}

type PhaseReview struct {
	Phase            PhaseRef          `json:"phase" required:"true"`
	ProjectID        string            `json:"project_id" required:"true"`
	ProjectName      string            `json:"project_name" required:"true"`
	WorkflowNumber   int               `json:"workflow_number" required:"true"`
	GeneratedAt      time.Time         `json:"generated_at" required:"true"`
	Validators       []ValidatorResult `json:"validators" required:"true"`
	Tasks            []ReviewTask      `json:"tasks" required:"true"`
	Findings         []Finding         `json:"findings" required:"true"`
	Summary          string            `json:"summary" required:"true"`
	Recommendation   string            `json:"recommendation,omitempty"`
	Diff             DiffSummary       `json:"diff" required:"true"`
	ApprovalID       string            `json:"approval_id,omitempty"`
	RequiresOverride bool              `json:"requires_override_reason" required:"true"`
}

type ReopenTasksRequest struct {
	ID        string   `path:"id" required:"true"`
	Elevation string   `header:"X-Waves-Elevation"`
	TaskIDs   []string `json:"task_ids" required:"true"`
	Reason    string   `json:"reason" required:"true"`
}

type ConfirmValidationRequest struct {
	ID        string `path:"id" required:"true"`
	VID       string `path:"vid" required:"true"`
	Elevation string `header:"X-Waves-Elevation"`
	Index     int    `json:"index" required:"true"`
}

// ---- approvals ----

type ApprovalSummary struct {
	ID            string     `json:"id" required:"true"`
	Category      string     `json:"category" required:"true" enum:"workflow,phase_start,phase_completion,task_execution,risky_action,result"`
	CategoryLabel string     `json:"category_label" required:"true"`
	State         string     `json:"state" required:"true" enum:"pending,approved,rejected,cancelled"`
	ProjectID     string     `json:"project_id" required:"true"`
	ProjectName   string     `json:"project_name" required:"true"`
	Subject       string     `json:"subject" required:"true"`
	RequestedBy   string     `json:"requested_by" required:"true"`
	RequestedAt   time.Time  `json:"requested_at" required:"true"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
}

type ApprovalFact struct {
	Label string `json:"label" required:"true"`
	Value string `json:"value" required:"true"`
	Mono  bool   `json:"mono,omitempty"`
}

type ApprovalDetail struct {
	ApprovalSummary
	Action            string         `json:"action" required:"true"`
	Facts             []ApprovalFact `json:"facts" required:"true"`
	Impact            string         `json:"impact" required:"true"`
	Commands          []string       `json:"commands" required:"true"`
	SubjectHash       string         `json:"subject_hash" required:"true"`
	AllowedDecisions  []string       `json:"allowed_decisions" required:"true"`
	StepUp            string         `json:"step_up" required:"true" enum:"every_time,window_ok"`
	RejectNeedsStepUp bool           `json:"reject_needs_stepup" required:"true"`
	PhaseID           string         `json:"phase_id,omitempty"`
}

type ApprovalListRequest struct {
	State     string `query:"state" enum:"pending,approved,rejected,cancelled"`
	ProjectID string `query:"project_id"`
	ListQuery
}

type ApprovalList struct {
	Items      []ApprovalSummary `json:"items" required:"true"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

type DecisionRequest struct {
	ID             string `path:"id" required:"true"`
	Elevation      string `header:"X-Waves-Elevation"`
	Decision       string `json:"decision" required:"true" enum:"approve,reject"`
	SubjectHash    string `json:"subject_hash" required:"true"`
	Feedback       string `json:"feedback,omitempty"`
	OverrideReason string `json:"override_reason,omitempty"`
}

// ---- executions ----

type ExecutionSummary struct {
	ID          string     `json:"id" required:"true"`
	TaskID      string     `json:"task_id" required:"true"`
	TaskTitle   string     `json:"task_title" required:"true"`
	TaskCode    string     `json:"task_code" required:"true"`
	ProjectID   string     `json:"project_id" required:"true"`
	ProjectName string     `json:"project_name" required:"true"`
	Harness     string     `json:"harness" required:"true"`
	Mode        string     `json:"mode" required:"true" enum:"harness_driven,waves_driven"`
	State       string     `json:"state" required:"true" enum:"queued,starting,running,waiting_approval,paused,completed,failed,cancelled,interrupted,disconnected"`
	StateDetail string     `json:"state_detail,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	DurationS   *int       `json:"duration_s,omitempty"`
}

type ExecutionListRequest struct {
	ProjectID string `query:"project_id"`
	State     string `query:"state"`
	TaskID    string `query:"task_id"`
	ListQuery
}

type ExecutionList struct {
	Items      []ExecutionSummary `json:"items" required:"true"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type Lease struct {
	ID                string    `json:"id" required:"true"`
	ExpiresAt         time.Time `json:"expires_at" required:"true"`
	HeartbeatInterval int       `json:"heartbeat_interval_s" required:"true"`
}

type Usage struct {
	TokensIn  int      `json:"tokens_in" required:"true"`
	TokensOut int      `json:"tokens_out" required:"true"`
	CostUSD   *float64 `json:"cost_usd,omitempty"`
}

type StateChange struct {
	At     time.Time `json:"at" required:"true"`
	State  string    `json:"state" required:"true" enum:"queued,starting,running,waiting_approval,paused,completed,failed,cancelled,interrupted,disconnected,blocked"`
	Detail string    `json:"detail,omitempty"`
}

type ExecutionEvent struct {
	At      time.Time `json:"at" required:"true"`
	Kind    string    `json:"kind" required:"true"`
	Message string    `json:"message" required:"true"`
}

type Orphan struct {
	Count   int    `json:"count" required:"true"`
	Command string `json:"command" required:"true"`
}

type Recovery struct {
	PartialFiles   int           `json:"partial_files" required:"true"`
	PartialAdded   int           `json:"partial_added" required:"true"`
	PartialRemoved int           `json:"partial_removed" required:"true"`
	LogLines       int           `json:"log_lines" required:"true"`
	LastLogAt      time.Time     `json:"last_log_at" required:"true"`
	Orphan         *Orphan       `json:"orphan,omitempty"`
	ResumeSession  string        `json:"resume_session,omitempty"`
	Reason         string        `json:"reason" required:"true"`
	Timeline       []StateChange `json:"timeline" required:"true"`
}

type ExecutionDetail struct {
	ExecutionSummary
	HarnessVersion  string           `json:"harness_version,omitempty"`
	Lease           *Lease           `json:"lease,omitempty"`
	WorkspacePath   string           `json:"workspace_path" required:"true"`
	WorkspaceLocked bool             `json:"workspace_locked" required:"true"`
	StartCommit     string           `json:"start_commit,omitempty"`
	Usage           *Usage           `json:"usage,omitempty"`
	History         []StateChange    `json:"history" required:"true"`
	Events          []ExecutionEvent `json:"events" required:"true"`
	DiffFiles       int              `json:"diff_files" required:"true"`
	Recovery        *Recovery        `json:"recovery,omitempty"`
	QueuedReason    string           `json:"queued_reason,omitempty"`
	Error           string           `json:"error,omitempty"`
	Result          string           `json:"result,omitempty"`
}

type LogRequest struct {
	ID     string `path:"id" required:"true"`
	Offset int    `query:"offset"`
}

type ContextSection struct {
	Title   string `json:"title" required:"true"`
	Content string `json:"content" required:"true"`
}

type ContextPackage struct {
	GeneratedAt time.Time        `json:"generated_at" required:"true"`
	SizeBytes   int              `json:"size_bytes" required:"true"`
	Sections    []ContextSection `json:"sections" required:"true"`
}

type ResolveRequest struct {
	ID       string `path:"id" required:"true"`
	Decision string `json:"decision" required:"true" enum:"fail,complete_partial,resume,retry_fresh"`
}

// ---- harnesses and guard ----

type Capability struct {
	Name  string `json:"name" required:"true"`
	Value string `json:"value" required:"true" enum:"supported,partial,unsupported,unknown"`
	Note  string `json:"note" required:"true"`
}

type Harness struct {
	ID                    string       `json:"id" required:"true"`
	Name                  string       `json:"name" required:"true"`
	Version               string       `json:"version,omitempty"`
	Installed             bool         `json:"installed" required:"true"`
	ConnectedProjects     []string     `json:"connected_projects" required:"true"`
	AvailableForExecution bool         `json:"available_for_execution" required:"true"`
	Capabilities          []Capability `json:"capabilities" required:"true"`
}

type HarnessList struct {
	Items []Harness `json:"items" required:"true"`
}

type GuardInfo struct {
	Policy            string `json:"policy" required:"true" enum:"off,audit,enforce"`
	Installed         bool   `json:"installed" required:"true"`
	Drift             string `json:"drift,omitempty"` // set when a recorded connection no longer matches the hook on disk
	WouldDeny24h      int    `json:"would_deny_24h" required:"true"`
	NotDefended       string `json:"not_defended" required:"true"`
	UnmanagedSessions string `json:"unmanaged_sessions" required:"true" enum:"allow,deny"`
}

type GuardDecision struct {
	At        time.Time `json:"at" required:"true"`
	Tool      string    `json:"tool" required:"true"`
	Target    string    `json:"target" required:"true"`
	Decision  string    `json:"decision" required:"true" enum:"allow,deny"`
	WouldDeny bool      `json:"would_deny" required:"true"`
	Reason    string    `json:"reason" required:"true"`
}

type GuardDecisionList struct {
	Items      []GuardDecision `json:"items" required:"true"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// ConnectRequest applies the plan the human reviewed: the service refuses it if
// the edits it would make now differ from the plan with this hash.
type ConnectRequest struct {
	ID        string `path:"id" required:"true"`
	Elevation string `header:"X-Waves-Elevation"`
	PlanHash  string `json:"plan_hash" required:"true"`
}

type RevokeRequest struct {
	Elevation string `header:"X-Waves-Elevation"`
}

type PatchProjectSettingsRequest struct {
	ID                string `path:"id" required:"true"`
	Elevation         string `header:"X-Waves-Elevation"`
	GuardPolicy       string `json:"guard_policy,omitempty" enum:"off,audit,enforce"`
	UnmanagedSessions string `json:"unmanaged_sessions,omitempty" enum:"allow,deny"`
}

type DisconnectRequest struct {
	ID        string `path:"id" required:"true"`
	Elevation string `header:"X-Waves-Elevation"`
}

type ConnectPlan struct {
	PlanHash string   `json:"plan_hash" required:"true"`
	File     string   `json:"file" required:"true"`
	Lines    []string `json:"lines" required:"true"`
	Applied  bool     `json:"applied" required:"true"`
}

// ---- settings ----

type Settings struct {
	PassphraseSet      bool       `json:"passphrase_set" required:"true"`
	ElevationWindowMin int        `json:"elevation_window_min" required:"true"`
	Listen             string     `json:"listen" required:"true"`
	LANEnabled         bool       `json:"lan_enabled" required:"true"`
	MaxConcurrent      int        `json:"max_concurrent" required:"true"`
	HardCap            int        `json:"hard_cap" required:"true"`
	HeartbeatS         int        `json:"heartbeat_interval_s" required:"true"`
	LogRetentionDays   int        `json:"log_retention_days" required:"true"`
	LogMaxMB           int        `json:"log_max_mb" required:"true"`
	SessionTTLHours    int        `json:"session_ttl_h" required:"true"`
	LastBackupAt       *time.Time `json:"last_backup_at,omitempty"`
	LastBackupBytes    int        `json:"last_backup_bytes,omitempty"`
}

type PatchSettingsRequest struct {
	Elevation          string `header:"X-Waves-Elevation"`
	ElevationWindowMin *int   `json:"elevation_window_min,omitempty"`
	MaxConcurrent      *int   `json:"max_concurrent,omitempty"`
	HeartbeatS         *int   `json:"heartbeat_interval_s,omitempty"`
	LogRetentionDays   *int   `json:"log_retention_days,omitempty"`
}

type BackupResult struct {
	At    time.Time `json:"at" required:"true"`
	Bytes int       `json:"bytes" required:"true"`
}

// ---- server-sent events (GET /events) ----

type EventHello struct {
	ServerVersion string `json:"server_version" required:"true"`
	AuditOK       bool   `json:"audit_ok" required:"true"`
}
type EventProjectChanged struct {
	ProjectID string `json:"project_id" required:"true"`
}
type EventWorkflowChanged struct {
	ProjectID string `json:"project_id" required:"true"`
	VersionID string `json:"version_id" required:"true"`
	State     string `json:"state" required:"true"`
}
type EventPhaseChanged struct {
	PhaseID   string `json:"phase_id" required:"true"`
	ProjectID string `json:"project_id" required:"true"`
	From      string `json:"from" required:"true"`
	To        string `json:"to" required:"true"`
}
type EventTaskChanged struct {
	TaskID  string `json:"task_id" required:"true"`
	PhaseID string `json:"phase_id" required:"true"`
	From    string `json:"from" required:"true"`
	To      string `json:"to" required:"true"`
}
type EventExecutionChanged struct {
	ExecutionID string `json:"execution_id" required:"true"`
	TaskID      string `json:"task_id" required:"true"`
	ProjectID   string `json:"project_id" required:"true"`
	From        string `json:"from" required:"true"`
	To          string `json:"to" required:"true"`
}
type EventExecutionLog struct {
	ExecutionID string `json:"execution_id" required:"true"`
	Offset      int    `json:"offset" required:"true"`
}
type EventApprovalChanged struct {
	ApprovalID string `json:"approval_id" required:"true"`
	ProjectID  string `json:"project_id" required:"true"`
	Category   string `json:"category" required:"true"`
	State      string `json:"state" required:"true"`
}
type EventAttentionChanged struct {
	Count int `json:"count" required:"true"`
}
type EventHarnessChanged struct {
	HarnessID string `json:"harness_id" required:"true"`
	ProjectID string `json:"project_id,omitempty"`
}
type EventGuardDenial struct {
	ProjectID string `json:"project_id" required:"true"`
	WouldDeny int    `json:"would_deny" required:"true"`
}
type EventAuditAlert struct {
	Kind string `json:"kind" required:"true" enum:"chain_broken,stepup_failures,bypass_attempt"`
}
type EventReset struct{}

// EventPayloads maps each SSE event name to its payload. It only exists so the
// generated types carry the event catalogue; the stream itself is not JSON.
type EventPayloads struct {
	Hello            EventHello            `json:"hello" required:"true"`
	Reset            EventReset            `json:"reset" required:"true"`
	ProjectChanged   EventProjectChanged   `json:"project.changed" required:"true"`
	WorkflowChanged  EventWorkflowChanged  `json:"workflow.changed" required:"true"`
	PhaseChanged     EventPhaseChanged     `json:"phase.changed" required:"true"`
	TaskChanged      EventTaskChanged      `json:"task.changed" required:"true"`
	ExecutionChanged EventExecutionChanged `json:"execution.changed" required:"true"`
	ExecutionLog     EventExecutionLog     `json:"execution.log" required:"true"`
	ApprovalChanged  EventApprovalChanged  `json:"approval.changed" required:"true"`
	AttentionChanged EventAttentionChanged `json:"attention.changed" required:"true"`
	HarnessChanged   EventHarnessChanged   `json:"harness.changed" required:"true"`
	GuardDenial      EventGuardDenial      `json:"guard.denial" required:"true"`
	AuditAlert       EventAuditAlert       `json:"audit.alert" required:"true"`
}
