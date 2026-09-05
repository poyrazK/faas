/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsTenantUsageApp } from './ObsTenantUsageApp.js';
/**
 * Monthly usage rollup for one tenant.
 */
export type ObsTenantUsage = {
  month: string;
  used_gb_hours: number;
  included_gb_hours: number;
  overage_gb_hours: number;
  overage_cents: number;
  used_cpu_hours: number;
  used_egress_gb: number;
  used_ingress_gb: number;
  cold_boots: number;
  requests: number;
  apps: Array<ObsTenantUsageApp>;
};

