/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresMaintenanceWindow } from './ManagedPostgresMaintenanceWindow.js';
/**
 * Changes to the database's weekly maintenance schedule.
 */
export type SetManagedPostgresMaintenanceRequest = {
  /**
   * Use null to remove the schedule.
   */
  window: (ManagedPostgresMaintenanceWindow | null);
};

