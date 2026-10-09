/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SyntheticCheckResults } from './SyntheticCheckResults.js';
/**
 * One synthetic check definition (ADR-748).
 */
export type SyntheticCheckResponse = {
  id: string;
  name: string;
  method: 'GET' | 'HEAD';
  path: string;
  /**
   * The full URL each probe requests.
   */
  url: string;
  /**
   * Exact expected status; absent means any 2xx.
   */
  expected_status?: number;
  timeout_ms: number;
  interval_seconds: 300 | 900 | 3600;
  enabled: boolean;
  created_at: string;
  results?: SyntheticCheckResults;
};

