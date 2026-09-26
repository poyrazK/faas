/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentCloneResponse } from './ProjectEnvironmentCloneResponse.js';
/**
 * Durable named environment target for a project.
 */
export type ProjectEnvironmentResponse = {
  id: string;
  project_id: string;
  /**
   * Project environment slug; the reserved app scope `default` cannot be used.
   */
  slug: string;
  protected: boolean;
  /**
   * GitHub pull request number for preview environments.
   */
  preview_pr_number?: number;
  /**
   * Full lowercase commit SHA recorded for the preview environment.
   */
  preview_head_sha?: string;
  created_at: string;
  updated_at: string;
  cloned_from?: string;
  clone?: ProjectEnvironmentCloneResponse;
};

