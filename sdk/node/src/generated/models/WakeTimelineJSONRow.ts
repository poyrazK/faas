/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One row of `AppWakeTimelineResponse.rows`. Mirrors
 * pkg/dashboard/views.WakeTimelineRow's fields so the JSON
 * mirror can render the same dashboard page 1:1. The
 * nullable fields (Trigger / QueuedCount / ConcurrencyAtAdmit
 * / ReadyInMS) use omitempty so the dashboard SPA can
 * distinguish "absent" (jsonb key missing — pre-PR-A fleet
 * row) from "explicit zero" (jsonb key present and 0).
 * `ready_in_ms = -1` is the em-dash sentinel for "no
 * boot_completed row yet" (still booting or rejected) — the
 * dashboard SPA renders "—" on -1, mirroring the HTML page
 * cell-empty branch.
 *
 */
export type WakeTimelineJSONRow = {
  /**
   * Event kind. Today always wake.boot_started; the field is open for future wake.boot_completed/_failed rows.
   */
  kind: 'wake.boot_started';
  /**
   * Mirror of the instance.state column on the per-app-detail recent-wakes table.
   */
  state: string;
  /**
   * RFC3339 UTC timestamp of the wake.
   */
  at?: string;
  /**
   * Wake-attempt correlation ID.
   */
  wake_id?: string;
  /**
   * Closed-enum trigger that admitted the wake (manual.cron / manual.api / scheduled.idle / …). Empty/absent on pre-PR-A fleet rows.
   */
  trigger?: string;
  /**
   * Wake method (restore or cold_boot), when telemetry is available.
   */
  method?: string;
  /**
   * Snapshot tier selected for the wake.
   */
  tier?: 'warm' | 'init' | 'cold_boot_fallback';
  /**
   * ledger.Concurrency at admit. 0 when absent.
   */
  queued_count?: number;
  /**
   * Same reading; 0 is the cold-start case.
   */
  concurrency_at_admit?: number;
  /**
   * True when admitted at the plan's per-app MaxConcurrency ceiling. Only meaningful when at_capacity_present is true.
   */
  at_capacity: boolean;
  /**
   * True when the at_capacity key was in jsonb; false = absent (pre-PR-A fleet). The dashboard renders em-dash when false.
   */
  at_capacity_present: boolean;
  /**
   * Wall-clock boot_started → boot_completed delta in ms. -1 when still booting or rejected. 0 is impossible (a 0ms wake would round to a positive integer).
   */
  ready_in_ms?: number;
};

