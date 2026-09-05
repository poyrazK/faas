/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One app placed on a node, with its resident footprint.
 */
export type ObsNodeApp = {
  id: string;
  slug: string;
  account_id: string;
  status: string;
  instances_live: number;
  instances_running: number;
  instances_waking: number;
  instances_cold_booting: number;
  ram_used_mb: number;
  last_request_at?: string | null;
};

