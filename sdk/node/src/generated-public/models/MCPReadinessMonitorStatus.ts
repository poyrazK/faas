/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessMonitorObservation } from './MCPReadinessMonitorObservation.js';
import type { MCPReadinessOAuthConnection } from './MCPReadinessOAuthConnection.js';
/**
 * Current policy-bound monitoring state and recent probe history for a deployment role.
 */
export type MCPReadinessMonitorStatus = {
  /**
   * Readiness environment. Omission selects default.
   */
  scope?: string;
  oauth_connection?: MCPReadinessOAuthConnection;
  recovery_action?: 'reconnect' | 'review_binding' | 'review_connection';
  deployment_id: string;
  role: string;
  policy_revision: string;
  artifact_sha256?: string;
  next_probe_at: string;
  last_success_at?: string;
  consecutive_failures: number;
  failure_threshold: number;
  state: 'pending' | 'healthy' | 'degraded' | 'firing' | 'stale';
  reason?: string;
  history: Array<MCPReadinessMonitorObservation>;
};

