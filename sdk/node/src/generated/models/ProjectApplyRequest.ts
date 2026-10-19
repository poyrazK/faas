/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectInitialSecrets } from './ProjectInitialSecrets.js';
/**
 * Multipart body for POST /v1/projects (apply). Same shape as
 * ProjectScanRequest; the apply handler resolves AppIDs and
 * inserts crons in a follow-up pass.
 *
 */
export type ProjectApplyRequest = {
  /**
   * Apply the reviewed migration naming mode, preserving repository service identities and project-prefixed app allocations. Automatic triggers and repository push bindings must be disabled.
   */
  migration?: boolean;
  /**
   * Reviewed repository service-to-app allocations for this apply. Preserve existing project app names and repeat the exact selected names from review; multipart clients encode this object as JSON.
   */
  app_slugs?: Record<string, string>;
  source: Blob;
  project_slug?: string;
  repo_full_name?: string;
  production_branch?: string;
  install_id?: number;
  only?: string;
  /**
   * Environment slug applied to deployments created by this apply
   */
  environment?: string;
  /**
   * Short-lived approval credential for the exact protected-environment plan
   */
  approval_token?: string;
  /**
   * Leave trigger and async-route declarations and existing project trigger/route state unchanged for this apply.
   */
  no_triggers?: boolean;
  /**
   * Override the customer identity policy for selected workloads (Hobby and above). Must match the scan override. Omitted uses Compose declarations and preserves existing app policy.
   */
  platform_tenant_required?: boolean;
  initial_secrets?: ProjectInitialSecrets;
};

