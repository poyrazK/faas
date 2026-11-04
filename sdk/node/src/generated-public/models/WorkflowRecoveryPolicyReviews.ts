/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryPolicyReview } from './WorkflowRecoveryPolicyReview.js';
/**
 * One identifier-ordered page of retained recovery policy reviews.
 */
export type WorkflowRecoveryPolicyReviews = {
  reviews: Array<WorkflowRecoveryPolicyReview>;
  next_after?: string;
};

