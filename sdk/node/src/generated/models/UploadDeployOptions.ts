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
  /**
   * Informational repository provenance URL; never fetched by apid.
   */
  source_url?: string;
  /**
   * Lowercase hexadecimal Git commit identifier.
   */
  commit_sha?: string;
  reason?: string;
  tag?: string;
  deployed_by?: string;
  pr_number?: number;
  workflows?: Array<WorkflowSpec>;
  /**
   * Resumable deploy policy persisted with deploy_options; Pro/Scale may enable first-wake 5xx auto-rollback, while omitted or null keeps the default false.
   */
  rollback_on_5xx?: boolean | null;
};

