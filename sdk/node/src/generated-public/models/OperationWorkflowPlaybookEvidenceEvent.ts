/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubject } from './OperationSubject.js';
import type { OperationWorkflowPlaybookBusinessOutcome } from './OperationWorkflowPlaybookBusinessOutcome.js';
import type { OperationWorkflowPlaybookEvidenceChange } from './OperationWorkflowPlaybookEvidenceChange.js';
import type { OperationWorkflowPlaybookOutcomeChange } from './OperationWorkflowPlaybookOutcomeChange.js';
/**
 * OperationWorkflowPlaybookEvidenceEvent. Opt-in durable checklist evidence monitoring; advisory observations preserve human reviews and application business authority.
 */
export type OperationWorkflowPlaybookEvidenceEvent = {
  /**
   * OperationWorkflowPlaybookEvidenceEvent.deadline_at. Original expected-result deadline when configured.
   */
  deadline_at?: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.business_outcome. Immutable delivery-time result, including an unmet deadline observation. Mutable escalation evidence is stored separately.
   */
  business_outcome?: OperationWorkflowPlaybookBusinessOutcome;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.outcome_change. Optional grouped business-result change; requirement changes may be empty for an outcome-only notification.
   */
  outcome_change?: OperationWorkflowPlaybookOutcomeChange;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.watch_id. Pinned case identity or retained watch metadata.
   */
  watch_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.progress_id. Pinned case identity or retained watch metadata.
   */
  progress_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.platform_tenant_id. Exact evidence customer, included only for operator watches and deliveries.
   */
  platform_tenant_id?: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.subject. Pinned case identity or retained watch metadata.
   */
  subject: OperationSubject;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.workflow. Pinned case identity or retained watch metadata.
   */
  workflow: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.instance_id. Pinned case identity or retained watch metadata.
   */
  instance_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.contract_version. Pinned case identity or retained watch metadata.
   */
  contract_version: number;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.playbook_id. Pinned case identity or retained watch metadata.
   */
  playbook_id: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.playbook_version. Pinned case identity or retained watch metadata.
   */
  playbook_version: number;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.checked_at. Observed case evidence clock; this is polling, not an application transaction timestamp.
   */
  checked_at: string;
  /**
   * OperationWorkflowPlaybookEvidenceEvent.changes. Grouped changes in requirement status or diagnostic reason; excludes observation clocks and report counters.
   */
  changes: Array<OperationWorkflowPlaybookEvidenceChange>;
};

