/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeReplayJobOutcome } from './RealtimeReplayJobOutcome.js';
/**
 * Realtime Replay Job.
 */
export type RealtimeReplayJob = {
  id: string;
  group_id: string;
  state: 'running' | 'paused' | 'checking' | 'check_paused' | 'completed' | 'cancelled';
  rate: number;
  selected: number;
  processed: number;
  replayed: number;
  skipped: number;
  outcomes: Array<RealtimeReplayJobOutcome>;
  created_at: string;
  updated_at: string;
  error_count: number;
  /**
   * Empty until a processing failure; processing_failed is a safe diagnostic without database details.
   */
  last_error: string;
  dry_run: boolean;
  /**
   * Eligible checks in a dry-run report; zero for a real replay.
   */
  eligible: number;
  /**
   * Source dry-run report for a reviewed replay. History reference may outlive the report.
   */
  reviewed_job_id: string | null;
  /**
   * Single real replay started from this report. History reference may outlive the replay.
   */
  started_job_id: string | null;
};

