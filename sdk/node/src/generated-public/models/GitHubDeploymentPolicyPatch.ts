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
  preview_bot_prs?: boolean;
  preview_draft_prs?: boolean;
  preview_service_policy?: 'deny' | 'allow_marked';
  production_trigger?: 'webhook' | 'actions';
};

