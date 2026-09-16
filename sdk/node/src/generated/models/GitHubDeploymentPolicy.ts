/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer-owned project-level GitHub deployment behaviour.
 */
export type GitHubDeploymentPolicy = {
  project_id: string;
  /**
   * Repository-relative root used when the project has a root workload.
   */
  root_dir: string;
  /**
   * Exact paths, one-segment globs, or trailing ** directory patterns that do not trigger builds.
   */
  ignored_paths: Array<string>;
  preview_enabled: boolean;
  preview_ttl_hours: number;
};

