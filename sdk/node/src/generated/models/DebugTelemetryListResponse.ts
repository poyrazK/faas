/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugTelemetryListFilters } from './DebugTelemetryListFilters.js';
import type { DebugTelemetryRequestItem } from './DebugTelemetryRequestItem.js';
/**
 * Response from GET /v1/apps/{slug}/debug/requests (ADR-127).
 * `since` echoes the effective window used (after the plan's
 * `DebugTelemetryRetentionDays` clamp) so the dashboard can
 * surface a "you widened past the cap" tile.
 *
 */
export type DebugTelemetryListResponse = {
  /**
   * Effective window applied (e.g. '24h', '72h').
   */
  since: string;
  /**
   * Inclusive start of the pinned retention window.
   */
  window_start: string;
  /**
   * Exclusive end of the pinned retention window.
   */
  window_end: string;
  /**
   * True when the requested lookback exceeded the plan retention cap.
   */
  retention_clamped: boolean;
  /**
   * True when this page contains every retained row in the pinned window.
   */
  complete: boolean;
  /**
   * Opaque cursor for the next page; omitted when complete is true.
   */
  next_cursor?: string;
  filters: DebugTelemetryListFilters;
  requests: Array<DebugTelemetryRequestItem>;
};

