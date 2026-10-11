/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppHealthCapacity } from './AppHealthCapacity.js';
import type { AppHealthCheck } from './AppHealthCheck.js';
import type { AppHealthRequests } from './AppHealthRequests.js';
/**
 * Read-only observed health of default-scope HTTP serving workloads.
 */
export type AppHealthResponse = {
  app_id: string;
  status: 'healthy' | 'degraded' | 'unhealthy' | 'unknown';
  phase: 'serving' | 'idle' | 'starting' | 'deploying' | 'stopped' | 'not_deployed' | 'maintenance' | 'unsupported' | 'unknown';
  summary: string;
  scope: 'default';
  evaluated_at: string;
  /**
   * Maximum age before clients must reconfirm this assessment.
   */
  valid_for_seconds: number;
  /**
   * Actual telemetry sample time; absent when unconfirmed.
   */
  metrics_as_of?: string;
  serving_deployment_ids: Array<string>;
  latest_deployment_id?: string;
  capacity: AppHealthCapacity;
  checks: Array<AppHealthCheck>;
  requests?: AppHealthRequests;
};

