/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeWAFReviewGroup } from './EdgeWAFReviewGroup.js';
/**
 * An app's sampled kind=waf detections grouped for a false-positive review.
 */
export type EdgeWAFReviewResponse = {
  /**
   * Start of the reviewed window
   */
  since: string;
  /**
   * Detection events read.
   */
  scanned: number;
  /**
   * The window held more detections than a review reads (5,000 per outcome, newest first), so the oldest were left out.
   */
  truncated: boolean;
  groups: Array<EdgeWAFReviewGroup>;
};

