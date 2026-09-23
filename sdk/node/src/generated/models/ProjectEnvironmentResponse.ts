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
  created_at: string;
  updated_at: string;
  cloned_from?: string;
  clone?: ProjectEnvironmentCloneResponse;
};

