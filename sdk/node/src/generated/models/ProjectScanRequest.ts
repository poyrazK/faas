/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Multipart body for POST /v1/projects/scan (dry-run).
 */
export type ProjectScanRequest = {
  /**
   * tar.gz of the repo root.
   */
  source: Blob;
  /**
   * kebab slug; default = repo dir basename
   */
  project_slug?: string;
  /**
   * GitHub owner/name to persist for push reconciliation
   */
  repo_full_name?: string;
  production_branch?: string;
  /**
   * GitHub installation id (with --repository or --repo); 0 for unbound repos
   */
  install_id?: number;
  /**
   * CSV of workload names to include (others skipped)
   */
  only?: string;
};

