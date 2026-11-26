/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained maintenance interval with its creator and optional cancellation attribution.
 */
export type MCPReadinessMaintenanceWindow = {
  id: string;
  scope: string;
  starts_at: string;
  ends_at: string;
  reason: string;
  /**
   * Account that scheduled the window.
   */
  created_by: string;
  created_at: string;
  cancelled_at?: string;
  cancelled_by?: string;
  cancel_reason?: string;
};

