/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitPromotionArtifact } from './EnvironmentGitPromotionArtifact.js';
/**
 * One retained serving release assessed against the selected current target manifest.
 */
export type EnvironmentGitRecoveryRelease = {
  /**
   * Current artifact policy selection, independent of rollback eligibility and routing retention.
   */
  protection_status?: 'disabled' | 'expired' | 'outside_policy' | 'budget_exceeded' | 'protected';
  /**
   * Age deadline of current protection; a newer release or explicit policy change may displace it earlier.
   */
  artifacts_protected_until?: string;
  /**
   * Per-release charged layer bytes when currently protected; shared keys are charged once within each release.
   */
  retained_bytes?: number;
  /**
   * Immutable release UUID within the current Git binding history.
   */
  release_set_id: string;
  /**
   * Whether this release is the environment's current active selection.
   */
  active: boolean;
  /**
   * Creation time used with release UUID for stable descending recovery pagination.
   */
  created_at: string;
  /**
   * Routing compatibility deadline for this release; does not guarantee artifact retention.
   */
  expires_at?: string;
  /**
   * Current selection or compatibility window status, separate from rollback eligibility.
   */
  retention_status: 'active' | 'unknown' | 'compatibility_window' | 'compatibility_window_elapsed';
  /**
   * Original manifest commit verified by historical serving evidence, or empty when unproven.
   */
  commit_sha: string;
  /**
   * Completion time of the selected release's historical serving receipt.
   */
  served_at?: string;
  /**
   * Whether the reused rollback planner reports no blockers at observation time.
   */
  eligibility: 'eligible' | 'blocked';
  /**
   * Value-redacted rollback planner blockers for this retained release.
   */
  blocking_reasons: Array<string>;
  /**
   * Public immutable image and signed build identities checked for this recovery selection.
   */
  artifacts: Array<EnvironmentGitPromotionArtifact>;
  /**
   * Individual rollback observation hash; obtain a fresh plan and revalidate before export.
   */
  plan_hash?: string;
};

