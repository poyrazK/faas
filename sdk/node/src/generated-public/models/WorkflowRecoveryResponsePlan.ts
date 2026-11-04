/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRecoveryResponseAction } from './WorkflowRecoveryResponseAction.js';
/**
 * Saved reviewed actions pinned to an app and parent recovery job. Explicit apply uses stable per-action identifiers. Actions commit independently.
 */
export type WorkflowRecoveryResponsePlan = {
  version: number;
  app: string;
  app_id: string;
  job_id: string;
  key: string;
  created_at: string;
  actions: Array<WorkflowRecoveryResponseAction>;
};

