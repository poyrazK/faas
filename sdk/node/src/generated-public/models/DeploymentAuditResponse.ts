/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of the deployment_audit timeline (E.2 + production-leveling Stream
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
   * Deployment audit event kind. Includes the current deployment_audit CHECK values and retained legacy SDK spellings.
   */
  kind: 'deploy.created' | 'deploy.traffic_changed' | 'deploy.rolled_back' | 'deploy.rolled_forward' | 'deploy.stuck' | 'deploy.recovered' | 'deploy.mcp_admission' | 'deploy.source_ref' | 'deploy.local_tarball' | 'deploy.health_probe_failed' | 'deploy.health_recovered' | 'deploy.removed' | 'deploy.rollout_started' | 'deploy.rollout_completed' | 'deploy.rollout_aborted' | 'deploy.canary_step_advanced' | 'deploy.alert_rule_fired' | 'deploy.scan_regressed' | 'deploy.mcp_task_held' | 'deploy.mcp_task_retry_requested' | 'deploy.mcp_task_retry_started' | 'deploy.mcp_task_retry_completed' | 'deploy.mcp_task_recovered' | 'deploy.mcp_task_hold_cancelled' | 'deploy.worker_drain_started' | 'deploy.worker_drain_completed';
  /**
   * Service-account UUID or operator CLI sentinel (`operator:cli:recover_rollout`, `meterd:safedeploy`, `meterd:canary_progression`). Operators identify who did what from this column.
   */
  actor: string;
  /**
   * Verbatim jsonb payload at emit time. For deploy.mcp_admission, mcp_admission contains MCPAdmissionEvidence: a committed or rejected traffic decision with policy digests and gateway replay metadata. No private JWT actions or credentials are retained.
   */
  data?: any;
  /**
   * Owning account UUID (cross-tenant IDOR posture).
   */
  account_id?: string;
  /**
   * When the audit row was stamped by an alert-rule-fired action (e.g. auto-rollback via the metering service ActionDispatcher), this carries the alert_rules.id UUID. nil for non-rule-triggered rows. Wire-additive — null for all older rows.
   */
  alert_rule_id?: string;
};

