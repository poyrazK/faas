/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Sidecar } from './Sidecar.js';
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
   * Named environment scope applied when the upload is committed; omitted uses default.
   */
  scope?: string;
  /**
   * Informational repository provenance URL; never fetched by apid.
   */
  source_url?: string;
  /**
   * Lowercase hexadecimal Git commit identifier.
   */
  commit_sha?: string;
  /**
   * Registered project environment to resolve at upload commit.
   */
  environment?: string;
  reason?: string;
  tag?: string;
  deployed_by?: string;
  pr_number?: number;
  workflows?: Array<WorkflowSpec>;
  /**
   * Up to 2 stateless sidecars (1 init + 1 sidecar) carried across the resumable upload session.
   */
  sidecars?: Array<Sidecar>;
  /**
   * Resumable deploy policy persisted with deploy_options; Pro/Scale may enable first-wake 5xx auto-rollback, while omitted or null keeps the default false.
   */
  rollback_on_5xx?: boolean | null;
};

