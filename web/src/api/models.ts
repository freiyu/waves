// Short names for the generated contract types. Nothing is declared here.
import type { components } from './types';

type S = components['schemas'];

export type ErrorBody = S['ContractErrorBody'];
export type Session = S['ContractSession'];
export type Elevation = S['ContractElevation'];
export type StepUpResponse = S['ContractStepUpResponse'];
export type LinkStatus = S['ContractLinkStatus'];
export type Status = S['ContractStatusResponse'];
export type AttentionItem = S['ContractAttentionItem'];
export type NextStep = S['ContractNextStep'];
export type AuditStatus = S['ContractAuditStatus'];
export type AuditRecord = S['ContractAuditRecord'];
export type AuditList = S['ContractAuditList'];
export type Project = S['ContractProject'];
export type ProjectDetail = S['ContractProjectDetail'];
export type PhaseRef = S['ContractPhaseRef'];
export type TaskSummary = S['ContractTaskSummary'];
export type Requirement = S['ContractRequirement'];
export type PlanningRequest = S['ContractPlanningRequest'];
export type Proposal = S['ContractProposal'];
export type WorkflowInfo = S['ContractWorkflowInfo'];
export type WorkflowVersion = S['ContractWorkflowVersion'];
export type WorkflowPhase = S['ContractWorkflowPhase'];
export type Validator = S['ContractValidator'];
export type PhaseReview = S['ContractPhaseReview'];
export type ValidatorResult = S['ContractValidatorResult'];
export type ApprovalSummary = S['ContractApprovalSummary'];
export type ApprovalDetail = S['ContractApprovalDetail'];
export type ExecutionSummary = S['ContractExecutionSummary'];
export type ExecutionDetail = S['ContractExecutionDetail'];
export type ContextPackage = S['ContractContextPackage'];
export type Harness = S['ContractHarness'];
export type GuardInfo = S['ContractGuardInfo'];
export type GuardDecision = S['ContractGuardDecision'];
export type ConnectPlan = S['ContractConnectPlan'];
export type Settings = S['ContractSettings'];
export type EventPayloads = S['ContractEventPayloads'];

export type ExecutionState = ExecutionSummary['state'];
export type ApprovalCategory = ApprovalSummary['category'];
export type WorkflowTask = S['ContractWorkflowTask'];
export type ReviewTask = S['ContractReviewTask'];
export type Recovery = S['ContractRecovery'];
