/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact-environment notification silence with a finite time range and audit reason.
 */
export type CreateMCPReadinessMaintenanceRequest = {
  scope?: string;
  /**
   * Omit to start now. Past starts older than one minute are rejected.
   */
  starts_at?: string;
  /**
   * Future end within 720 hours of start.
   */
  ends_at: string;
  /**
   * Nonblank UTF-8 reason of at most 500 bytes.
   */
  reason: string;
};

