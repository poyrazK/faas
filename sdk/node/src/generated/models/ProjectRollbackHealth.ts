/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable outcome of the configured HTTP health probes for the exact restored route.
 */
export type ProjectRollbackHealth = {
  /**
   * One of pending, awaiting_gateways, awaiting_probe, stabilizing, passed, or failed.
   */
  status: string;
  deadline?: string;
  checked_at?: string;
  /**
   * First successful probe in the continuous stabilization window.
   */
  healthy_since?: string;
  route_generation?: number;
};

