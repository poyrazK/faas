/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained customer event counts, logical JSON bytes, pending age and current account budgets.
 */
export type EventStorageUsageResponse = {
  retained_events: number;
  retained_bytes: number;
  pending_events: number;
  oldest_pending_at: string | null;
  limits: {
    retained_events: number;
    retained_bytes: number;
    /**
     * How long a settled receipt is kept on the plan; it bounds duplicate acceptance, recovery and replay ranges. Absent from servers that predate per-plan windows.
     */
    settled_retention_seconds?: number;
  };
};

