/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionTaskRef } from './ExecutionTaskRef.js';
import type { ResolvedExecutionLimits } from './ResolvedExecutionLimits.js';
/**
 * Published task configuration, lifecycle status and version metadata.
 */
export type ExecutionTaskVersionResponse = (ExecutionTaskRef & {
  runtime: string;
  profile: string;
  environment_id?: string;
  limits: ResolvedExecutionLimits;
  input_schema?: any;
  result_schema?: any;
  integration_ids?: Array<string>;
  output_files?: Array<string>;
  artifact_storage?: 'inline' | 'stored';
  agent_endpoint_ids?: Array<string>;
  status: 'active' | 'retired';
  created_at: string;
});

