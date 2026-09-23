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
  /**
   * Controls calls from project previews to production internal
   * services. `deny` rejects the call before discovery or wake-up;
   * `allow_marked` permits it and marks the request as preview-origin
   * traffic. Projects created before this policy was introduced are
   * migration-backed to `allow_marked`.
   *
   */
  preview_service_policy: 'deny' | 'allow_marked';
};

