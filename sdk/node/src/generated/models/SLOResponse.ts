/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SLOStatus } from './SLOStatus.js';
/**
 * One customer-defined SLO (ADR-747).
 */
export type SLOResponse = {
  id: string;
  name: string;
  sli: 'availability' | 'latency';
  /**
   * Threshold for a latency SLO; absent for availability.
   */
  latency_threshold_ms?: number;
  objective_pct: number;
  window_days: 7 | 30;
  created_at: string;
  status?: SLOStatus;
};

