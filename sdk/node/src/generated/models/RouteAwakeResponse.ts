/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAwakeRoute } from './RouteAwakeRoute.js';
/**
 * How long the app was awake in the window and which routes kept it awake (ADR-1187). Seconds are instance-seconds of billed running time.
 */
export type RouteAwakeResponse = {
  slug: string;
  from: string;
  until: string;
  window_clamped?: boolean;
  /**
   * The app's effective idle timeout: how long it stays awake after its last successful request.
   */
  idle_timeout_seconds: number;
  window_seconds: number;
  awake_seconds: number;
  /**
   * Share of the window's minutes with at least one instance running.
   */
  awake_share: number;
  /**
   * Billed MB-seconds in the window.
   */
  mb_seconds: number;
  gb_hours: number;
  /**
   * Cold boots in the window.
   */
  wakes: number;
  /**
   * Awake time no recorded request explains: synthetic probes, scheduled prewarm, minimum instances, queue, cron and waitUntil work, or telemetry the plan's rate cap dropped.
   */
  unattributed_seconds: number;
  /**
   * Awake time attributed to routes outside the busiest routes_limit.
   */
  other_routes_seconds: number;
  routes_limit: number;
  routes: Array<RouteAwakeRoute>;
};

