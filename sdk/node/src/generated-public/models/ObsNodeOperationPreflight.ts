/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded impact summary captured before a provider compute-node lifecycle intent is enqueued.
 */
export type ObsNodeOperationPreflight = {
  affected_apps: number;
  affected_tenants: number;
  total_instances: number;
  live_instances: number;
  live_ram_mb: number;
  /**
   * Signed admission-capacity delta if the requested lifecycle transition lands; zero for an idempotent request.
   */
  capacity_change_mb: number;
  reversible: boolean;
  /**
   * True when a force-drain targets a node that still has live instances.
   */
  disruption_warning: boolean;
};

