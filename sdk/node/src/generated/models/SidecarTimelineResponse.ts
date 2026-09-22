/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { SidecarTimelineStatus } from './SidecarTimelineStatus.js';
import type { WakeTimelineEvent } from './WakeTimelineEvent.js';
/**
 * Envelope for GET /v1/apps/{slug}/sidecars/{sidecar_name}/timeline.
 */
export type SidecarTimelineResponse = {
  /**
   * Echo of the sidecar_name path segment.
   */
  sidecar_name: string;
  /**
   * Resolved app id (the slug's owning app).
   */
  app_id: string;
  latest?: SidecarTimelineStatus;
  events: Array<WakeTimelineEvent>;
  /**
   * Opaque RFC 3339 cursor for the next page. Empty when this is the last page.
   */
  next_cursor?: string;
  /**
   * Effective limit applied (always 1..1000).
   */
  limit: number;
};

