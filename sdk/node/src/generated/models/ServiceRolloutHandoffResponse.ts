/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable scheduler progress for a zero-downtime service rollout routing and request-drain handoff.
 */
export type ServiceRolloutHandoffResponse = {
  action: 'promote' | 'abort';
  phase: 'pending' | 'routing' | 'draining' | 'complete';
  predecessor_deployment_id?: string;
  generation?: number;
  expected_gateways?: Array<string>;
  acknowledged_gateways?: Array<string>;
  missing_gateways?: Array<string>;
  retry_count: number;
  last_error?: string;
  reason?: string;
  started_at?: string | null;
  updated_at?: string | null;
  acknowledged_at?: string | null;
  completed_at?: string | null;
};

