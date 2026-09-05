/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-node capacity - resident and reserved footprint against the node size.
 */
export type ObsCapacityNode = {
  id: string;
  name: string;
  active: boolean;
  vpcpus: number;
  vcpu_budget: number;
  mem_mb: number;
  admission_ceiling_mb: number;
  instances_live: number;
  instances_running: number;
  instances_waking: number;
  instances_cold_booting: number;
  ram_used_mb: number;
  admission_margin_mb: number;
  apps_count: number;
  tenants_count: number;
};

