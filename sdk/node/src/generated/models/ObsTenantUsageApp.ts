/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app usage split inside a tenant month.
 */
export type ObsTenantUsageApp = {
  app_id: string;
  app_slug?: string;
  mb_seconds: number;
  cpu_usec: number;
  requests: number;
  tx_bytes: number;
  net_tx_bytes: number;
  net_rx_bytes: number;
  cold_boots: number;
};

