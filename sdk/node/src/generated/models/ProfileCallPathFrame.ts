/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileSourceLocation } from './ProfileSourceLocation.js';
/**
 * One sampled frame in a complete saved caller path. Automatic canary evidence retains only safe repository-relative paths and line numbers; source URLs are derived at read time from each deployment's recorded commit.
 */
export type ProfileCallPathFrame = {
  name: string;
  file?: string;
  line?: number;
  /**
   * Safe repository-relative baseline path retained for automatic canary evidence.
   */
  readonly baseline_path?: string;
  readonly baseline_line?: number;
  /**
   * Safe repository-relative candidate path retained for automatic canary evidence.
   */
  readonly candidate_path?: string;
  readonly candidate_line?: number;
  /**
   * Request-time source link for the stable deployment; omitted when source is unavailable.
   */
  readonly baseline_source?: ProfileSourceLocation;
  /**
   * Request-time source link for the canary deployment; omitted when source is unavailable.
   */
  readonly candidate_source?: ProfileSourceLocation;
};

