/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentConfigDiffResponse } from './ProjectEnvironmentConfigDiffResponse.js';
import type { ProjectEnvironmentPromotionChange } from './ProjectEnvironmentPromotionChange.js';
import type { ProjectEnvironmentQualificationResponse } from './ProjectEnvironmentQualificationResponse.js';
import type { ProjectReleaseSetResponse } from './ProjectReleaseSetResponse.js';
/**
 * Read-only promotion preview between two registered project environments.
 */
export type ProjectEnvironmentPromotionPreviewResponse = {
  project_slug: string;
  from_environment: string;
  to_environment: string;
  /**
   * Whether this exact promotion will copy the source's non-secret configuration to the target.
   */
  sync_config?: boolean;
  to_environment_protected: boolean;
  approval_required: boolean;
  can_promote: boolean;
  blocking_reasons?: Array<string>;
  config_diff: ProjectEnvironmentConfigDiffResponse;
  changes: Array<ProjectEnvironmentPromotionChange>;
  /**
   * Immutable active release graph snapshot for the source environment, when present.
   */
  from_release_set?: ProjectReleaseSetResponse;
  /**
   * Immutable active release graph snapshot for the target environment, when present.
   */
  to_release_set?: ProjectReleaseSetResponse;
  /**
   * True when promotion stages deployments dark and atomically activates a project release graph.
   */
  release_graph_mode: boolean;
  /**
   * Compatibility window used by the promoted release graph.
   */
  release_ttl_seconds?: number;
  /**
   * True when a protected-target promotion requires a fresh source qualification.
   */
  qualification_required?: boolean;
  /**
   * Latest qualification for the active source release set, including failed or expired receipts.
   */
  qualification?: ProjectEnvironmentQualificationResponse;
  promotion_hash: string;
  promotion_token: string;
};

