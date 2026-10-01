/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable compute locality with admission policy and observed node state. No capacity reservation is implied.
 */
export type ComputeRegionResponse = {
  id: string;
  display_name: string;
  /**
   * Operator policy for new app admission; independent of node health.
   */
  accepting_new_apps: boolean;
  /**
   * Whether at least one node passes static placement eligibility; does not report spare capacity.
   */
  availability: 'available' | 'unavailable';
  /**
   * Heartbeat freshness among eligible nodes; unknown when no eligible node has reported a heartbeat. Existing lifecycle reconciliation remains authoritative for admission.
   */
  observed_health: 'healthy' | 'degraded' | 'unavailable' | 'unknown';
  registered_nodes: number;
  active_nodes: number;
  eligible_nodes: number;
  healthy_nodes: number;
};

