/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Connected GitHub repository input for POST /v1/projects/scan/source-ref.
 */
export type ProjectSourceRefScanRequest = {
  /**
   * GitHub owner/name to fetch through the connected installation.
   */
  repo: string;
  /**
   * Branch, tag, or commit ref to scan.
   */
  ref: string;
  project_slug: string;
  /**
   * Repository binding stored if the plan is later applied; defaults to repo.
   */
  repo_full_name?: string;
  production_branch?: string;
  /**
   * Optional connected installation id. Omit to resolve the single installation that can access repo.
   */
  install_id?: number;
  only?: Array<string>;
  exclude?: Array<string>;
  /**
   * Environment slug applied to deployments created by this scan
   */
  environment?: string;
  no_triggers?: boolean;
};

