/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * OperationWorkflowPlaybookApplicability. Ephemeral applicability of a durable pinned checklist against current exact-case evidence. No progress, review history, readiness or outcome is mutated.
 */
export type OperationWorkflowPlaybookApplicability = {
  /**
   * OperationWorkflowPlaybookApplicability.status. Current advisory disposition; absent triggers and terminal reports are not business success.
   */
  status: 'applicable' | 'trigger_absent' | 'terminal_reported' | 'contract_changed' | 'guidance_changed' | 'evidence_unavailable' | 'definition_unavailable' | 'evidence_incomplete';
  /**
   * OperationWorkflowPlaybookApplicability.summary. Evidence interpretation, including uncertainty and authority boundaries.
   */
  summary: string;
  /**
   * OperationWorkflowPlaybookApplicability.suggested_action. Human review suggestion; never execution permission or an automatic checklist closure.
   */
  suggested_action: string;
  /**
   * OperationWorkflowPlaybookApplicability.checked_at. Shared server observation clock for the current case checklists; sources are independently read.
   */
  checked_at: string;
  /**
   * OperationWorkflowPlaybookApplicability.current_contract_version. Current projected workflow contract version, if a report exists; status determines comparability.
   */
  current_contract_version?: number;
  /**
   * OperationWorkflowPlaybookApplicability.state_revision. Observed application state revision, when available; stale reports remain explicitly unavailable.
   */
  state_revision?: number;
  /**
   * OperationWorkflowPlaybookApplicability.reporting_operation_id. Current state-report operation, when available; distinct from the frozen original reporting operation.
   */
  reporting_operation_id?: string;
  /**
   * OperationWorkflowPlaybookApplicability.definition_id. Exact current reporting definition validated under account, application, customer, environment and subject fences, if available.
   */
  definition_id?: string;
  /**
   * OperationWorkflowPlaybookApplicability.definition_changed. An observed reporting definition differs from the pinned original. False does not establish availability; inspect definition_id and status.
   */
  definition_changed: boolean;
  /**
   * OperationWorkflowPlaybookApplicability.trigger_matched. The unchanged pinned rule positively matches current retained evidence. False may mean unevaluated or incomplete evidence; inspect status.
   */
  trigger_matched: boolean;
  /**
   * OperationWorkflowPlaybookApplicability.evidence_complete. Relevant trigger coverage is complete for this retained evaluation. Does not validate existing checklist attachments or infer business correctness.
   */
  evidence_complete: boolean;
  /**
   * OperationWorkflowPlaybookApplicability.remaining_item_count. Count of frozen checklist items not human-reviewed as completed; independent of current trigger or business outcome.
   */
  remaining_item_count: number;
};

