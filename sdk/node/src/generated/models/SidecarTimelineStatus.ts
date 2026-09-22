/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Most recent health transition for a sidecar, if one has been recorded.
 */
export type SidecarTimelineStatus = {
  /**
   * RFC 3339 UTC timestamp of the health transition.
   */
  at: string;
  /**
   * Closed sidecar health state emitted by guest-init.
   */
  status: 'starting' | 'healthy' | 'unhealthy' | 'restarting' | 'failed';
  /**
   * Optional producer-supplied transition reason.
   */
  reason?: string;
};

