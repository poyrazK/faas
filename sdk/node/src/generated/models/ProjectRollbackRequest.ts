/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Select production revisions to restore and configure rollback admission and verification.
 */
export type ProjectRollbackRequest = {
  all?: boolean;
  acknowledge_releases?: boolean;
  /**
   * Check configured HTTP health probes after each cutover before continuing.
   */
  verify?: boolean;
  /**
   * Verify configured HTTP probes of all prepared revisions at zero traffic before the first cutover. Requires verify.
   */
  verify_before_cutover?: boolean;
  /**
   * Minimum healthy probe duration in seconds after each cutover. Nonzero values require verify.
   */
  stabilize_for_seconds?: number;
  /**
   * Verify configured production 5xx budgets after cutover before advancing. Requires verify.
   */
  verify_live_traffic?: boolean;
  /**
   * Automatically undo switched services on a confirmed health probe 5xx. Requires verify.
   */
  undo_on_health_failure?: boolean;
  /**
   * Automatically undo switched services on a reconfirmed production 5xx budget breach. Requires verify_live_traffic.
   */
  undo_on_live_traffic_failure?: boolean;
  /**
   * Stable start request UUID. Repeat with the same options to read the accepted operation.
   */
  request_id?: string;
  /**
   * Plan hash from a runnable preview with the same rollback options.
   */
  expected_plan_hash?: string;
  /**
   * Recorded production deploy group to restore. Omit all when selecting a deploy group.
   */
  to_deploy?: string;
};

