/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A persisted durable workflow run (ADR-081).
 */
export type WorkflowRunResponse = {
  id: string;
  app_id: string;
  workflow_name: string;
  status: 'pending' | 'running' | 'awaiting_event' | 'succeeded' | 'failed' | 'dead';
  current_step?: string | null;
  input?: any;
  output?: any;
  scheduled_for: string;
  started_at?: string | null;
  finished_at?: string | null;
  last_error?: string | null;
  created_at: string;
  updated_at: string;
};

