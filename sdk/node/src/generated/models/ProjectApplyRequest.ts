/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Multipart body for POST /v1/projects (apply). Same shape as
 * ProjectScanRequest; the apply handler resolves AppIDs and
 * inserts crons in a follow-up pass.
 *
 */
export type ProjectApplyRequest = {
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
   * Leave trigger declarations and existing project trigger state unchanged for this apply.
   */
  no_triggers?: boolean;
};

