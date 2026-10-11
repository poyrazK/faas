/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BindingCheckFinding } from './BindingCheckFinding.js';
/**
 * Durable progress of a checked historical rollback. This receipt grants no authority to reuse binding evidence or move traffic for another operation.
 */
export type RollbackOperation = {
  id: string;
  app_id: string;
  scope: string;
  target_deployment_id: string;
  current_deployment_id: string;
  status: 'preparing' | 'ready' | 'blocked' | 'routing' | 'complete' | 'failed';
  service: boolean;
  reason?: string;
  code?: string;
  blockers?: Array<BindingCheckFinding>;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
  audit_id?: string;
};

