/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Deployment metadata persisted with the upload session and applied at commit.
 */
export type UploadDeployOptions = {
  runtime?: string;
  handler?: string;
  dockerfile?: boolean;
  source_root?: string;
  reason?: string;
  tag?: string;
  deployed_by?: string;
  pr_number?: number;
  workflows?: Array<WorkflowSpec>;
};

