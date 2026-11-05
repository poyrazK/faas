/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOperationCheck } from './AppOperationCheck.js';
/**
 * Read-only operation authorization snapshot with separate admission limitations.
 */
export type AppOperationPreview = {
  schema_version: 1;
  app_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  operation: 'deploy' | 'restart' | 'scale' | 'env_change';
  authorization: 'allowed' | 'blocked' | 'unknown';
  admission: 'allowed' | 'blocked' | 'unknown';
  required_scopes: Array<string>;
  checks: Record<string, AppOperationCheck>;
  checked_at: string;
};

