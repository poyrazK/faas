/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionDigestPolicy } from './OperationWorkflowNextActionDigestPolicy.js';
import type { OperationWorkflowPlaybookEvidenceWatch } from './OperationWorkflowPlaybookEvidenceWatch.js';
import type { OperationWorkflowReadinessImpactWatch } from './OperationWorkflowReadinessImpactWatch.js';
/**
 * Durable subscription configuration and most recent successful bounded evaluation; deletion cancels future evaluations and removes its inbox.
 */
export type OperationWorkflowReadinessSubscription = {
  /**
   * Next digest evaluation time. Evaluation occurs on a worker pass at or after this time; failures retry without consuming the cadence.
   */
  digest_next_at?: string;
  /**
   * Most recent successful digest evaluation, including silent evaluations with no changed items.
   */
  digest_evaluated_at?: string;
  /**
   * Most recent changed-item digest delivery. This does not acknowledge or fulfill source work.
   */
  digest_delivered_at?: string;
  /**
   * Configured team digest filters and cadence for this subscription scope; delete and recreate the subscription to change this policy.
   */
  next_action_digest?: OperationWorkflowNextActionDigestPolicy;
  /**
   * OperationWorkflowReadinessSubscription.business_impact_watches. Retained impact observations and coverage status for configured protected results.
   */
  business_impact_watches?: Array<OperationWorkflowReadinessImpactWatch>;
  /**
   * OperationWorkflowReadinessSubscription.playbook_evidence_watches. Durable opt-in checklist evidence monitoring, independent of readiness discovery filters.
   */
  playbook_evidence_watches?: Array<OperationWorkflowPlaybookEvidenceWatch>;
  /**
   * OperationWorkflowReadinessSubscription.app_id. Application UUID; required in customer requests and omitted in account requests.
   */
  app_id: string;
  /**
   * OperationWorkflowReadinessSubscription.tenant_id. Optional account-only customer selector; customer identity derives from authentication.
   */
  tenant_id?: string;
  /**
   * OperationWorkflowReadinessSubscription.scope. Application environment scope.
   */
  scope: string;
  /**
   * OperationWorkflowReadinessSubscription.name. Subscription name.
   */
  name: string;
  /**
   * OperationWorkflowReadinessSubscription.workflow. Optional declared workflow selector.
   */
  workflow?: string;
  /**
   * OperationWorkflowReadinessSubscription.target_operation. Optional transition operation selector.
   */
  target_operation?: string;
  /**
   * OperationWorkflowReadinessSubscription.owner. Optional blocker or pending verification owner selector.
   */
  owner?: string;
  /**
   * OperationWorkflowReadinessSubscription.unassigned. Select ownerless blockers or verification obligations.
   */
  unassigned?: boolean;
  /**
   * OperationWorkflowReadinessSubscription.events. Readiness inbox event selectors; defaults to all four when omitted.
   */
  events: Array<'newly_ready' | 'newly_blocked' | 'reassigned' | 'unavailable'>;
  /**
   * OperationWorkflowReadinessSubscription.id. Subscription UUID.
   */
  id: string;
  /**
   * OperationWorkflowReadinessSubscription.created_at. Subscription creation time.
   */
  created_at: string;
  /**
   * OperationWorkflowReadinessSubscription.evaluated_at. Most recent successful evaluation time.
   */
  evaluated_at: string;
  /**
   * OperationWorkflowReadinessSubscription.status. Evaluation health; retrying preserves the last successful baseline.
   */
  status: 'active' | 'retrying';
  /**
   * OperationWorkflowReadinessSubscription.watched_transition_count. Current saved edges within the 100-edge watch budget.
   */
  watched_transition_count: number;
  /**
   * OperationWorkflowReadinessSubscription.coverage_incomplete. Discovery, evidence previews or saved-edge capacity are incomplete; no exhaustive delivery guarantee is inferred.
   */
  coverage_incomplete: boolean;
  /**
   * OperationWorkflowReadinessSubscription.latest_sequence. Latest committed inbox sequence.
   */
  latest_sequence: number;
};

