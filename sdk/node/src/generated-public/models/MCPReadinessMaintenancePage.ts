/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessMaintenanceWindow } from './MCPReadinessMaintenanceWindow.js';
/**
 * Bounded page of maintenance windows ordered by creation time and ID.
 */
export type MCPReadinessMaintenancePage = {
  windows: Array<MCPReadinessMaintenanceWindow>;
  next_before?: string;
};

