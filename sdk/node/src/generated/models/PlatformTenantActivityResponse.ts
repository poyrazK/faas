/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlatformTenantActivityFilters } from './PlatformTenantActivityFilters.js';
import type { PlatformTenantActivityItem } from './PlatformTenantActivityItem.js';
/**
 * A retention-bounded page of observed debugger evidence. Page totals weight each collapsed telemetry row by request.count and must not be treated as complete usage.
 */
export type PlatformTenantActivityResponse = {
  tenant_id: string;
  /**
   * Actual lookback duration after applying the account's debugger retention ceiling.
   */
  since: string;
  window_start: string;
  window_end: string;
  plan_retention_days: number;
  retention_clamped: boolean;
  page_telemetry_rows: number;
  page_represented_requests: number;
  page_error_requests: number;
  /**
   * True when this page contains all matching retained rows in the pinned window; it does not imply complete telemetry capture.
   */
  page_complete: boolean;
  /**
   * Present when another page of retained rows exists.
   */
  next_cursor?: string;
  filters: PlatformTenantActivityFilters;
  requests: Array<PlatformTenantActivityItem>;
};

