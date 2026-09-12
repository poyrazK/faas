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
};

