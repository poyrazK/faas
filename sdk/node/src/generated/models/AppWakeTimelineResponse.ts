/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WakeTimelineApp } from './WakeTimelineApp.js';
import type { WakeTimelineJSONRow } from './WakeTimelineJSONRow.js';
/**
 * JSON mirror of the dashboard per-app wake-timeline page.
 * Plan-gated Hobby+ (same code as /v1/apps/{slug}/metrics:
 * plan_per_app_metrics_not_allowed).
 *
 * `trigger_histogram` is a JSON object (map[string]int) — empty
 * `{}` on a fresh app, never null. The dashboard SPA must
 * treat missing keys as 0 (JSON.parse() returns undefined for
 * missing keys, not 0 — the render code adds the explicit
 * `?? 0` fallback).
 *
 * `at_capacity_pct` is the share of `wake_count_with_meta`
 * rows where the events.wake.boot_started join succeeded AND
 * the at_capacity flag is true. Pre-PR-A fleet rows
 * contribute to `wake_count_24h` but not the denominator.
 *
 */
export type AppWakeTimelineResponse = {
  app: WakeTimelineApp;
  /**
   * Number of instance rows in the trailing 24h window (after the descending-cutoff break; the endpoint examines up to 100 recent rows).
   */
  wake_count_24h: number;
  /**
   * Denominator for at_capacity_pct — count of rows where the events.wake.boot_started LEFT JOIN succeeded.
   */
  wake_count_with_meta: number;
  /**
   * Numerator for at_capacity_pct.
   */
  at_capacity_count: number;
  /**
   * Share of meta-bearing rows admitted at the per-app MaxConcurrency ceiling.
   */
  at_capacity_pct: number;
  /**
   * trigger → N count of WakeBootMeta.Trigger values across the meta-bearing rows. Empty {} on a fresh app, never null.
   */
  trigger_histogram: Record<string, number>;
  /**
   * trigger_class → N count of known user/monitor/crawler/preview_bot/unknown classifications. Empty {} on a fresh app, never null.
   */
  trigger_class_histogram: Record<string, number>;
  /**
   * Wake rows in DESC StartedAt order, truncated at the 24h cutoff (descending-cutoff break) and capped at 100 recent rows.
   */
  rows: Array<WakeTimelineJSONRow>;
  /**
   * RFC3339Nano UTC timestamp marking the JSON envelope's authoritative 'as of' instant.
   */
  as_of: string;
};

