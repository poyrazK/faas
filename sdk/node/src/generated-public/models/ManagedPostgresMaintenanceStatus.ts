/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresBaseRefresh } from './ManagedPostgresBaseRefresh.js';
import type { ManagedPostgresMaintenanceWindow } from './ManagedPostgresMaintenanceWindow.js';
/**
 * Configured maintenance window and the current database refresh status.
 */
export type ManagedPostgresMaintenanceStatus = {
  window: (ManagedPostgresMaintenanceWindow | null);
  /**
   * Start of the current or next window.
   */
  window_start?: string;
  /**
   * Exclusive end of the current or next window.
   */
  window_end?: string;
  window_open: boolean;
  /**
   * First stale observation for the pending base refresh.
   */
  stale_since?: string;
  last_refresh?: ManagedPostgresBaseRefresh;
};

