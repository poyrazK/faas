/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the deployment_audit timeline (issue #976 /
 * ADR-122 / SAFE-RELEASES-E.2 + production-leveling Stream
 * A). Data is the verbatim jsonb payload at emit time;
 * kind-specific shape — DeployTrafficChanged carries
 * {from_percent, to_percent, actor_kind}, DeployRolledBack
 * carries {target_deployment_id, reason}.
 *
 */
export type DeploymentAuditResponse = {
  /**
   * RFC3339Nano UTC timestamp at which the row was emitted. Sequence-pointed — sorting by (at DESC) gives the canonical timeline order.
   */
  at: string;
  /**
   * Closed-set event kind (migrations/00477 enforces the same CHECK on the deployments_audit table).
   */
  kind: 'deploy.created' | 'deploy.traffic_changed' | 'deploy.rolled_back' | 'deploy.rolled_forward' | 'deploy.stuck' | 'deploy.recovered';
  /**
   * Service-account UUID or operator CLI sentinel (`operator:cli:recover_rollout`, `meterd:safedeploy`, `meterd:canary_progression`). Operators identify who did what from this column.
   */
  actor: string;
  /**
   * Verbatim jsonb payload at emit time (kind-specific shape — see description).
   */
  data?: any;
  /**
   * Owning account UUID (cross-tenant IDOR posture).
   */
  account_id?: string;
  /**
   * SAFE-RELEASES-OBS PR-D (issue #976 / ADR-122): when the audit row was stamped by an alert-rule-fired action (e.g. auto-rollback via meterd ActionDispatcher), this carries the alert_rules.id UUID. nil for non-rule-triggered rows. Wire-additive per ADR-016 — null for all pre-PR-D rows.
   */
  alert_rule_id?: string;
};

