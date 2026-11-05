/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Assign and schedule one exact pending fulfillment-review cycle with a reason. Source revision and review fingerprint fence stale edits. Preserve source status, owner, note and deadline; do not combine with other actions. Configure an optional escalation team/grace period; reassignment after an overdue reminder cancels escalation for that deadline, including reverted routing. Removing the deadline disables escalation when policy fields are omitted. Empty owner explicitly unassigns; omitted due_at clears the review deadline. Original fulfillment remains intact.
 */
export type OperationWorkflowFulfillmentReviewCoordinationRequest = {
  /**
   * Omission preserves existing policy when the review owner is unchanged and a deadline remains. Empty string clears escalation. Nonempty team must differ from the review owner and requires an explicit deadline and positive grace period.
   */
  escalation_owner?: string;
  /**
   * Optional grace period after actual overdue-reminder delivery. Omission preserves the existing value; zero clears escalation. Owner reassignment or removing the deadline clears policy when both policy fields are omitted.
   */
  escalate_after_seconds?: number;
  review_id: string;
  review_fingerprint: string;
  owner: string;
  due_at?: string;
  reason: string;
};

