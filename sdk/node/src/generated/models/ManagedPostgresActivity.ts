/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresActivityCause } from './ManagedPostgresActivityCause.js';
import type { ManagedPostgresActivityComputeStart } from './ManagedPostgresActivityComputeStart.js';
import type { ManagedPostgresActivityHint } from './ManagedPostgresActivityHint.js';
/**
 * Why a database was awake in a period (ADR-20261125231222). Causes are best guesses from timing.
 */
export type ManagedPostgresActivity = {
  database_id: string;
  database_name: string;
  /**
   * Start of the period after clamping to the 30-day retention.
   */
  since: string;
  until: string;
  /**
   * Recorded compute starts in the period.
   */
  starts: number;
  /**
   * Seconds the compute was up in the period; a start still running counts to until.
   */
  awake_seconds: number;
  /**
   * The suspend timeout in force; omitted for an always-on database or when it could not be read.
   */
  suspend_timeout_seconds?: number;
  causes: Array<ManagedPostgresActivityCause>;
  /**
   * The latest starts, newest first, at most 200.
   */
  compute_starts: Array<ManagedPostgresActivityComputeStart>;
  /**
   * True when the period had more starts than compute_starts lists; the totals and causes count them all.
   */
  truncated?: boolean;
  hints: Array<ManagedPostgresActivityHint>;
  docs_url: string;
};

