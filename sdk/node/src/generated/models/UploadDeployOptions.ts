/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DeploymentResourcesRequest } from './DeploymentResourcesRequest.js';
import type { DeploymentScalingRequest } from './DeploymentScalingRequest.js';
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
  resources?: DeploymentResourcesRequest;
  /**
   * At upload commit, set an immutable deployment serving-instance cap. Omit or use 0 to inherit app policy; the app/plan cap still aggregates across revisions.
   */
  max_instances?: number | null;
  scaling?: DeploymentScalingRequest;
  reason?: string;
  tag?: string;
  deployed_by?: string;
  pr_number?: number;
  workflows?: Array<WorkflowSpec>;
  /**
   * Preferred field for up to five helpers carried across the resumable upload session (one init helper and up to four long-running companions).
   */
  companions?: Array<Sidecar>;
  /**
   * Deprecated spelling of companions.
   * @deprecated
   */
  sidecars?: Array<Sidecar>;
  /**
   * Resumable deploy policy persisted with deploy_options; Pro/Scale may enable first-wake 5xx auto-rollback, while omitted or null keeps the default false.
   */
  rollback_on_5xx?: boolean | null;
  /**
   * Create this deployment without temporary startup CPU headroom; omitted or null keeps the default boost.
   */
  disable_startup_cpu_boost?: boolean | null;
  /**
   * Skip reconciling trigger declarations from the uploaded gregale manifest at commit time.
   */
  no_triggers?: boolean;
};

