/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataConnectionIssue } from './DataConnectionIssue.js';
import type { DataPipelineCapacity } from './DataPipelineCapacity.js';
import type { DataSourceTable } from './DataSourceTable.js';
/**
 * Durable, bounded compatibility observation. Capacity separately reports measured, operator-qualified allocations; a passed compatibility check does not establish production readiness.
 */
export type DataConnectionCheck = {
  capacity?: DataPipelineCapacity;
  id: string;
  connection_id: string;
  connection_revision: number;
  database_id?: string;
  state: 'pending' | 'running' | 'passed' | 'blocked' | 'stale';
  fresh: boolean;
  truncated: boolean;
  checks: Array<DataConnectionIssue>;
  tables: Array<DataSourceTable>;
  created_at: string;
  observed_at?: string;
  expires_at?: string;
};

