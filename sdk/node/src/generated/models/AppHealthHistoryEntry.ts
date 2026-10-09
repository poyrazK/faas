/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthResponse } from './AppHealthResponse.js';
/**
 * Original baseline, meaningful transition, or expired-evidence gap; times do not imply continuous incident duration.
 */
export type AppHealthHistoryEntry = {
  id: string;
  kind: 'baseline' | 'transition' | 'gap';
  /**
   * Assessment time, or previous evidence expiry for a gap entry.
   */
  observed_at: string;
  previous_status?: 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
  assessment: AppHealthResponse;
};

