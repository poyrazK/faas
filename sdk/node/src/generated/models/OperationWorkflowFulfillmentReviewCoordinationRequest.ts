/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Assign and schedule one exact pending fulfillment-review cycle with a reason. Source revision and review fingerprint fence stale edits. Preserve source status, owner, note and deadline; do not combine with other actions. Empty owner explicitly unassigns; omitted due_at clears the review deadline. Original fulfillment remains intact.
 */
export type OperationWorkflowFulfillmentReviewCoordinationRequest = {
  review_id: string;
  review_fingerprint: string;
  owner: string;
  due_at?: string;
  reason: string;
};

