/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRegressionItem } from './DebugRegressionItem.js';
import type { DebugRegressionReview } from './DebugRegressionReview.js';
/**
 * Updated regression plus the exact workflow receipt. Older servers may omit review.
 */
export type DebugRegressionActionResponse = {
  regression: DebugRegressionItem;
  review?: DebugRegressionReview;
};

