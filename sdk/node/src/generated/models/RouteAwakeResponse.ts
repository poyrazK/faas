/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAwakeRoute } from './RouteAwakeRoute.js';
import type { RouteAwakeSource } from './RouteAwakeSource.js';
/**
 * How long the app was awake in the window and which routes kept it awake. Seconds are instance-seconds of billed running time.
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
  /**
   * The plan's waitUntil task timeout when the plan enables waitUntil. An instance parks no earlier than the later of the idle timeout and its last waitUntil task, so a request keeps the app awake for the longer of the two.
   */
  wait_until_timeout_seconds?: number;
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
   * Awake time no recorded request, probe, prewarm or delivery explains: minimum instances, probes older than a day, deliveries already pruned by result retention, or telemetry the plan's rate cap dropped.
   */
  unattributed_seconds: number;
  /**
   * Awake time credited to causes other than customer requests, for minutes no request explains: route probes and prewarms, and cron, queue and other deliveries the scheduler made to the app, per cron job and per queue.
   */
  sources: Array<RouteAwakeSource>;
  /**
   * The app's current minimum instance count; above zero the app never parks, so unattributed time is expected.
   */
  min_instances: number;
  /**
   * Awake time attributed to routes outside the busiest routes_limit.
   */
  other_routes_seconds: number;
  routes_limit: number;
  routes: Array<RouteAwakeRoute>;
};

