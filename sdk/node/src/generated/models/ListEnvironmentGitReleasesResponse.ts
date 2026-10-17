/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EnvironmentGitRecoveryRelease } from './EnvironmentGitRecoveryRelease.js';
import type { EnvironmentRecoveryRetention } from './EnvironmentRecoveryRetention.js';
/**
 * Read-only page of recovery eligibility observations for one verified target manifest commit.
 */
export type ListEnvironmentGitReleasesResponse = {
  recovery_retention?: EnvironmentRecoveryRetention;
  /**
   * Current charged bytes across all protected releases of this source, including releases outside this page.
   */
  retention_bytes?: number;
  /**
   * Registered project slug for this recovery page.
   */
  project: string;
  /**
   * Registered environment slug for the retained recovery history.
   */
  environment: string;
  /**
   * Current Git binding identity restricting all releases and the page cursor.
   */
  source_id: string;
  /**
   * Provider-verified immutable target manifest commit for every release assessment in the page.
   */
  target_commit_sha: string;
  /**
   * Time of this page's recovery observation; eligibility is not a future guarantee.
   */
  observed_at: string;
  /**
   * Confirms that listing recovery history caused no retention or execution mutation.
   */
  read_only: boolean;
  /**
   * Confirms that recovery results omit configuration values, secret references and storage paths.
   */
  values_redacted: boolean;
  /**
   * Recovery proposals must still pass new qualification for the target environment.
   */
  requires_target_qualification: boolean;
  /**
   * Newest retained completed serving releases from this Git source, including expired windows.
   */
  releases: Array<EnvironmentGitRecoveryRelease>;
  /**
   * Last release UUID in this page when additional older recovery history exists.
   */
  next_before?: string;
};

