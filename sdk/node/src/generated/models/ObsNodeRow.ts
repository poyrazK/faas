/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One compute node with live utilization counters.
 */
export type ObsNodeRow = {
  id: string;
  name: string;
  active: boolean;
  vpcpus: number;
  mem_mb: number;
  max_concurrency: number;
  admission_ceiling_mb: number;
  overlay_ip?: string;
  last_heartbeat_at?: string;
  created_at: string;
  instances_live: number;
  instances_running: number;
  instances_waking: number;
  instances_cold_booting: number;
  ram_used_mb: number;
  admission_margin_mb: number;
  cpu_pct_60s?: number | null;
  disk_used_bytes?: number | null;
};

