/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Aggregate capacity counters across the fleet.
 */
export type ObsCapacitySummary = {
  total_nodes: number;
  active_nodes: number;
  inactive_nodes: number;
  total_vcpus: number;
  total_vcpu_budget: number;
  total_mem_mb: number;
  total_admission_ceiling_mb: number;
  ram_used_mb: number;
  admission_margin_mb: number;
  instances_live: number;
  instances_running: number;
  instances_waking: number;
  instances_cold_booting: number;
  apps_total: number;
  tenants_total: number;
  unplaced_apps: number;
};

