/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CheckedProjectReleaseManifest } from './CheckedProjectReleaseManifest.js';
/**
 * Pinned project release manifest and lifetime for temporary candidate service staging.
 */
export type StageProjectReleaseRequest = {
  manifest: CheckedProjectReleaseManifest;
  /**
   * Temporary billable capacity lease; repeating the same active stage does not extend it.
   */
  ttl_seconds: number;
};

