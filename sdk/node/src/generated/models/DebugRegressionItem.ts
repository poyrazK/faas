/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One regression observation row.
 */
export type DebugRegressionItem = {
  deployment_id: string;
  route: string;
  p95_ms: number;
  p95_base_ms: number;
  affected_count: number;
  /**
   * Decimal string with up to 2 places, NUMERIC(5,2).
   */
  regression_factor: string;
  first_detected_at: string;
  last_detected_at: string;
  state: 'active' | 'acknowledged' | 'dismissed' | 'resolved';
  acknowledged_at?: string;
  dismissed_until?: string;
  resolved_at?: string;
};

