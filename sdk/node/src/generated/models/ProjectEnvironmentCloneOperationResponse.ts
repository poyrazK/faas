/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentCloneResourceResponse } from './ProjectEnvironmentCloneResourceResponse.js';
/**
 * Durable clone progress within one account and project, excluding private source configuration and worker credentials.
 */
export type ProjectEnvironmentCloneOperationResponse = {
  operation_id: string;
  project_slug: string;
  source_environment: string;
  target_environment: string;
  source_revision_hash: string;
  source_release_set_id?: string;
  target_release_set_id?: string;
  status: 'pending' | 'capturing' | 'copying' | 'publishing' | 'ready' | 'failed' | 'compensating' | 'compensated';
  revision: number;
  resources: Array<ProjectEnvironmentCloneResourceResponse>;
  error_code?: string;
  created_at: string;
  updated_at: string;
};

