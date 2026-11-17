/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimePostCaptureClassCounts } from './ObsRuntimePostCaptureClassCounts.js';
import type { ObsRuntimePostCaptureCounts } from './ObsRuntimePostCaptureCounts.js';
import type { ObsRuntimePostCaptureItem } from './ObsRuntimePostCaptureItem.js';
/**
 * Advisory post-capture activation evidence for image workers in v2 profiles. Each replica reads the shared fleet independently; compare its view without summing deployment counts or inferring consensus. Gate classifications and ages are retained evidence, not a new eligibility or health verdict.
 */
export type ObsRuntimePostCaptureHealth = {
  scope: 'replica_observations_of_shared_fleet';
  state: 'nonempty' | 'empty' | 'incomplete' | 'unknown';
  reason?: 'post_capture_evidence_observed' | 'incomplete_post_capture_evidence' | 'post_capture_evidence_unavailable';
  counts: ObsRuntimePostCaptureCounts;
  classes: Array<ObsRuntimePostCaptureClassCounts>;
  incomplete: boolean;
  items: Array<ObsRuntimePostCaptureItem>;
  omitted_items: number;
};

