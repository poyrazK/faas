/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bulk action and version preconditions for a named schedule group.
 */
export type ManagedRealtimeScheduleGroupRequest = {
  /**
   * Exact schedule ID/version map of all pending or paused group members; terminal members excluded.
   */
  expected_versions: Record<string, number>;
};

