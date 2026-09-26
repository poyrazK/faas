/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Partial replacement for a project's GitHub deployment policy.
 */
export type GitHubDeploymentPolicyPatch = {
  root_dir?: string;
  ignored_paths?: Array<string>;
  preview_enabled?: boolean;
  preview_ttl_hours?: number;
  preview_service_policy?: 'deny' | 'allow_marked';
  /**
   * Set to an empty string to disable durable project-environment previews.
   */
  preview_environment_from?: string;
};

