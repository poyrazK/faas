/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitOpsPlan } from './EnvironmentGitOpsPlan.js';
import type { EnvironmentGitPromotionArtifact } from './EnvironmentGitPromotionArtifact.js';
import type { EnvironmentGitPromotionChange } from './EnvironmentGitPromotionChange.js';
import type { EnvironmentGitPromotionOrigin } from './EnvironmentGitPromotionOrigin.js';
import type { EnvironmentGitPromotionTarget } from './EnvironmentGitPromotionTarget.js';
/**
 * Read-only, value-redacted promotion proposal. It neither activates a release nor proves target readiness.
 */
export type PreviewEnvironmentGitPromotionResponse = {
  /**
   * Project slug.
   */
  project: string;
  /**
   * Source environment slug.
   */
  from_environment: string;
  /**
   * Target environment slug.
   */
  to_environment: string;
  /**
   * Observation time.
   */
  observed_at: string;
  /**
   * Always true; no writes or leases are issued.
   */
  read_only: boolean;
  /**
   * Configuration values and secret-reference targets are omitted.
   */
  values_redacted: boolean;
  /**
   * Source evidence cannot authorize target activation.
   */
  requires_target_qualification: boolean;
  origin: EnvironmentGitPromotionOrigin;
  target: EnvironmentGitPromotionTarget;
  /**
   * Selected source deployment identities.
   */
  artifacts: Array<EnvironmentGitPromotionArtifact>;
  /**
   * Image-only source contract proposals.
   */
  changes: Array<EnvironmentGitPromotionChange>;
  target_plan: EnvironmentGitOpsPlan;
  /**
   * Reasons patch or manifest export is unavailable.
   */
  blocking_reasons: Array<string>;
  /**
   * Domain-separated hash of source release, artifacts and target observation; never an execution token.
   */
  plan_hash: string;
};

