/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigDiffResponse } from './ProjectEnvironmentConfigDiffResponse.js';
import type { ProjectEnvironmentPromotionChange } from './ProjectEnvironmentPromotionChange.js';
/**
 * Read-only promotion preview between two registered project environments.
 */
export type ProjectEnvironmentPromotionPreviewResponse = {
  project_slug: string;
  from_environment: string;
  to_environment: string;
  to_environment_protected: boolean;
  approval_required: boolean;
  can_promote: boolean;
  blocking_reasons?: Array<string>;
  config_diff: ProjectEnvironmentConfigDiffResponse;
  changes: Array<ProjectEnvironmentPromotionChange>;
  promotion_hash: string;
  promotion_token: string;
};

