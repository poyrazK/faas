/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/rollback. All fields optional. Without a body the handler falls back to rolling back to the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id` set, the handler validates that the named deployment belongs to this app and is superseded or live with zero traffic.
 */
export type RollbackRequest = {
  /**
   * The UUID or vN of the deployment to promote back to 'live'. Must belong to the same app as the URL slug, and be superseded or live with zero traffic. Nil/empty falls back to the most-recent superseded deployment (legacy behaviour).
   */
  target_deployment_id?: string;
  /**
   * When set, the handler stamps the deployment_audit row's alert_rule_id column with this UUID so an operator can cross-link the audit timeline back to /dashboard/alerts/{id}. Wire-additive; the field is ignored when nil/empty. Only privileged in-process callers (the metering service ActionDispatcher) set this; the API does not enforce role because the endpoint already requires MFA + ScopesDeployWrite.
   */
  alert_rule_id?: string;
  /**
   * Exact current deployment UUID or vN. Together with an explicit target starts the checked asynchronous workflow and returns rollback_operation. Rejects a changed current deployment instead of selecting another one.
   */
  expected_current_deployment_id?: string;
  /**
   * One-line reason for an exact checked rollback. Cannot be combined with alert_rule_id.
   */
  reason?: string;
  /**
   * Roll back although release commands ran after the target last served. Rollback does not undo them. Without it such a rollback is refused with rollback_crosses_release, which lists them. Automated rollbacks never set it.
   */
  acknowledge_releases?: boolean;
  /**
   * Also restore the managed PostgreSQL database the crossed release commands migrated to just before the earliest of them, and switch the app to that copy. Requires acknowledge_releases and the postgres:manage scope; cannot be combined with expected_current_deployment_id. The response carries database_switch; the code rollback happens inside it while the app is fenced.
   */
  restore_databases?: boolean;
};

