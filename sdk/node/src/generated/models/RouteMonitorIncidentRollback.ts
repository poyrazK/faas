/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The single automatic rollback decision for an incident when on_violation is rollback (ADR-943). claimed is transient while the checked rollback is requested.
 */
export type RouteMonitorIncidentRollback = {
  status: 'claimed' | 'requested' | 'skipped';
  reason?: 'latency_only_violation' | 'outside_rollback_window' | 'no_healthy_baseline' | 'rollback_target_ineligible';
  /**
   * First error-budget route that triggered the decision, as METHOD /path.
   */
  route?: string;
  target_deployment_id?: string;
  /**
   * Checked rollback operation readable at /v1/apps/{slug}/rollbacks/{operation}.
   */
  operation_id?: string;
  decided_at: string;
};

