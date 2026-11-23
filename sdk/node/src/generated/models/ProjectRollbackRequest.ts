/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
   * Require this many seconds of healthy post-cutover HTTP probes before advancing. A positive value requires verify.
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
   * Restore the production revisions recorded for this project deploy. Used without all.
   */
  to_deploy?: string;
};

