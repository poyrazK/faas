/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReplayBackfillProgress } from './EventReplayBackfillProgress.js';
/**
 * Progress and immutable selection details for one durable event backfill.
 */
export type EventReplayBackfillJobResponse = {
  id: string;
  app_slug: string;
  subscription_id: string;
  /**
   * Fingerprint of the subscription declaration snapshotted when the job was created.
   */
  subscription_revision: string;
  from: string;
  until: string;
  /**
   * Fixed exclusive acceptance cutoff.
   */
  cutoff_at: string;
  /**
   * Account-wide earliest event surviving when the job was created; does not establish complete coverage.
   */
  earliest_retained_at?: string;
  /**
   * Always false; retained receipts are not an archive.
   */
  history_complete: boolean;
  duplicate_policy: 'skip_existing';
  state: 'running' | 'completed' | 'completed_with_failures';
  /**
   * Whether the retained range has been fully examined.
   */
  scan_complete: boolean;
  progress: EventReplayBackfillProgress;
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

