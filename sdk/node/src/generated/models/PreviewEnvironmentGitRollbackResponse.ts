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
 * Read-only, value-redacted rollback proposal. It neither activates a release nor proves target readiness.
 */
export type PreviewEnvironmentGitRollbackResponse = {
  /**
   * Project containing the selected historical release.
   */
  project: string;
  /**
   * Environment whose workload code would be restored.
   */
  environment: string;
  /**
   * Time at which rollback selection and current target intent were observed.
   */
  observed_at: string;
  /**
   * True because rollback planning does not change pins, intent or deployment status.
   */
  read_only: boolean;
  /**
   * Rollback output excludes variable values and secret-reference destinations.
   */
  values_redacted: boolean;
  /**
   * Historical serving evidence selects code; the new target still needs fresh qualification.
   */
  requires_target_qualification: boolean;
  /**
   * Current active release identity, or empty when no release is active.
   */
  current_release_set_id: string;
  /**
   * Historical serving completion time; missing proof blocks export.
   */
  origin_served_at?: string;
  origin: EnvironmentGitPromotionOrigin;
  target: EnvironmentGitPromotionTarget;
  /**
   * Retained deployment and exact code artifact identities selected for rollback.
   */
  artifacts: Array<EnvironmentGitPromotionArtifact>;
  /**
   * Code-only source contract proposals.
   */
  changes: Array<EnvironmentGitPromotionChange>;
  target_plan: EnvironmentGitOpsPlan;
  /**
   * Historical evidence, artifact or target conflicts that prevent rollback export.
   */
  blocking_reasons: Array<string>;
  /**
   * Rollback-specific SHA256 of historical serving evidence, selected code and current target intent; never deployment authority.
   */
  plan_hash: string;
};

