/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetryJobHealthSample } from './EventExecutionLineageRetryJobHealthSample.js';
/**
 * Retained bulk retry job identity, creation guards, lifecycle status, and progress summary.
 */
export type EventExecutionLineageRetryJobSummary = {
  /**
   * Last known committed evaluation or skip; absent for unevaluated or legacy jobs.
   */
  last_progress_at?: string;
  attention?: EventExecutionLineageRetryJobHealthSample;
  request_id: string;
  subscription_id: string;
  app_id: string;
  status: 'pending' | 'paused' | 'completed' | 'cancelled';
  selected: number;
  /**
   * Cases evaluated or skipped; cancelled items are not counted.
   */
  processed: number;
  created_at: string;
  updated_at: string;
  retain_until: string;
};

