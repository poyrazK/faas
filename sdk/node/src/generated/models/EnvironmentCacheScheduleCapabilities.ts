/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Serving API and state backend support for named-environment schedule reads. These signals do not establish target authorization, migration health, worker readiness or compatibility of other replicas. Upgrade all schedule writers before relying on activity completeness.
 */
export type EnvironmentCacheScheduleCapabilities = {
  /**
   * Supports bounded current schedule discovery.
   */
  listing: boolean;
  /**
   * Supports contiguous scoped schedule activity pages and cursor expiry.
   */
  activity: boolean;
  /**
   * Supports capturing scoped feed bounds without scanning activity.
   */
  checkpoint: boolean;
  /**
   * Advertised activity retention window in seconds; zero means retention support is unavailable. Guided MCP recovery requires the documented 30-day contract.
   */
  retention_seconds: number;
};

