/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DevPostgresRequest } from './DevPostgresRequest.js';
/**
 * Application shape and opaque local identity for a CLI-managed developer environment.
 */
export type UpsertDevSessionRequest = {
  type?: 'app' | 'function';
  runtime?: 'node22' | 'python312' | 'go124' | 'go124-alpine' | 'node24' | 'python313';
  /**
   * Opaque identity derived locally from the CLI installation and canonical source path. Omit only for legacy sessions.
   */
  workspace_id?: string;
  postgres?: DevPostgresRequest;
  /**
   * How long the environment survives after this request. Omit for the 24-hour default. Values above the plan's developer lease maximum return 403 plan_limit_developer_lease.
   */
  lease_seconds?: number;
};

