/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RouteAwakeSuggestion } from './RouteAwakeSuggestion.js';
/**
 * Awake time one non-request cause explains.
 */
export type RouteAwakeSource = {
  /**
   * route_probe: synthetic canary probes the app answered (kept for a day). prewarm: instances a scheduled prewarm admitted. The others are deliveries the scheduler made to the app, by invocation source; a delivery counts from when it reached the instance until it completed, at most 60 minutes.
   */
  source: 'route_probe' | 'prewarm' | 'cron' | 'queue' | 'delayed_task' | 'async_invoke' | 'inbound_webhook' | 'replay' | 'esm';
  /**
   * One cron job (the path or command it runs and its schedule) or one queue; absent for other sources.
   */
  name?: string;
  /**
   * Probe requests answered without a 5xx or an auth rejection, prewarmed instances, or deliveries that started in the window.
   */
  events: number;
  awake_seconds: number;
  awake_share: number;
  gb_hours: number;
  /**
   * Set for cron jobs: periodic when the job fires at a steady interval.
   */
  pattern?: 'periodic' | 'irregular';
  /**
   * Approximate interval between the cron job's firings when pattern is periodic.
   */
  interval_seconds?: number;
  suggestion?: RouteAwakeSuggestion;
};

