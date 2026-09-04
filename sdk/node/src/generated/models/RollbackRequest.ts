/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body for POST /v1/apps/{slug}/rollback (SAFE-RELEASES-G, issue #976). All fields optional. Without a body the handler falls back to rolling back to the most-recent superseded deployment (pre-#976 behaviour). With `target_deployment_id` set, the handler validates that the named deployment belongs to this app AND has status='superseded'.
 */
export type RollbackRequest = {
  /**
   * The UUID of the deployment to promote back to 'live'. Must belong to the same app as the URL slug, and must have status='superseded'. Nil/empty falls back to the most-recent superseded deployment (legacy behaviour).
   */
  target_deployment_id?: string;
  /**
   * SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): when set, the handler stamps the deployment_audit row's alert_rule_id column with this UUID so an operator can cross-link the audit timeline back to /dashboard/alerts/{id}. Wire-additive per ADR-016; the field is ignored when nil/empty. Only privileged in-process callers (meterd ActionDispatcher) set this; the API does not enforce role because the endpoint already requires MFA + ScopesDeployWrite.
   */
  alert_rule_id?: string;
};

