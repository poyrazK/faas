/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained workload identity and durable route withdrawal and scheduler drain progress. Historical evidence does not grant execution authority.
 */
export type EnvironmentWorkloadRetirement = {
  id: string;
  account_id?: string;
  project_id?: string;
  environment_id: string;
  source_id: string;
  app_id: string;
  revision_id: string;
  resource: string;
  plan_hash?: string;
  job_id?: string;
  route_deployment_id?: string;
  recovery_revision_id?: string;
  phase: 'planned' | 'retiring' | 'retired' | 'recovering' | 'recovered' | 'cancelled';
  generation: number;
  route_generation?: number;
  recovery_generation?: number;
  deployment_ids?: Array<string>;
  queue_binding_ids?: Array<string>;
  expected_gateways?: Array<string>;
  acknowledged_gateways?: Array<string>;
  requested_at: string;
  retired_at?: string | null;
  recovered_at?: string | null;
};

