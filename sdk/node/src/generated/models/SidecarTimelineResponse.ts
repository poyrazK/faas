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
   * Identifier of the app that owns this sidecar timeline (resolved from the slug).
   */
  app_id: string;
  latest?: SidecarTimelineStatus;
  events: Array<WakeTimelineEvent>;
  /**
   * RFC 3339 timestamp cursor for the next sidecar page; empty when no more frames remain.
   */
  next_cursor?: string;
  /**
   * Number of sidecar frames returned, from 1 through 1000.
   */
  limit: number;
};

