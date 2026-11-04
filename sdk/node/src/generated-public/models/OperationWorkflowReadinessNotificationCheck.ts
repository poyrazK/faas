/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationWorkflowNextActionDigestCheck } from './OperationWorkflowNextActionDigestCheck.js';
import type { OperationWorkflowReadinessBusinessOutcome } from './OperationWorkflowReadinessBusinessOutcome.js';
import type { OperationWorkflowReadinessChange } from './OperationWorkflowReadinessChange.js';
import type { OperationWorkflowReadinessHandoff } from './OperationWorkflowReadinessHandoff.js';
import type { OperationWorkflowReadinessNotification } from './OperationWorkflowReadinessNotification.js';
/**
 * Current advisory evidence check preserving the original notification. Global failures remain unavailable.
 */
export type OperationWorkflowReadinessNotificationCheck = {
  next_action_digest?: OperationWorkflowNextActionDigestCheck;
  business_outcome?: OperationWorkflowReadinessBusinessOutcome;
  handoff: OperationWorkflowReadinessHandoff;
  notification: OperationWorkflowReadinessNotification;
  /**
   * OperationWorkflowReadinessNotificationCheck.checked_at. Current check time.
   */
  checked_at: string;
  /**
   * OperationWorkflowReadinessNotificationCheck.status. Digest_checked for per-item source reconciliation; ready or blocked only for a comparable retained edge; otherwise the explicit comparison boundary status.
   */
  status: string;
  change?: OperationWorkflowReadinessChange;
};

