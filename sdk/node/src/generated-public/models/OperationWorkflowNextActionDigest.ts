/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionDigestExplanation } from './OperationWorkflowNextActionDigestExplanation.js';
import type { OperationWorkflowNextActionQueueItem } from './OperationWorkflowNextActionQueueItem.js';
/**
 * One bounded changed-item team digest. Items are advisory snapshots, not application authorization; source state may change before reading or publication. Fingerprints exclude polling timestamps and are recorded atomically with delivery under the subscription lease. Acknowledgment changes no source obligation.
 */
export type OperationWorkflowNextActionDigest = {
  /**
   * Delivery explanations identified by source subscription, sequence and action kind. Older notifications may omit these.
   */
  explanations?: Array<OperationWorkflowNextActionDigestExplanation>;
  items: Array<OperationWorkflowNextActionQueueItem>;
  owner: string;
  scope: string;
  evaluated_at: string;
  soon_until: string;
  /**
   * All retained qualifying action rows, including unchanged items; not a workflow or global task count.
   */
  matching_count: number;
  /**
   * Newly qualifying or changed action rows pending delivery at this evaluation.
   */
  changed_count: number;
  /**
   * Changed rows beyond this delivery cap, which remain eligible at later cadence evaluations.
   */
  remaining_changed_count: number;
  /**
   * Queue and source inbox retention bound coverage. Deleted/pruned source work is unavailable.
   */
  retention_limited: boolean;
};

