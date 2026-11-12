/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The change hook delivery the app's runtime last reported.
 */
export type DataAPIChangeHookStatus = {
  /**
   * When the platform received the report.
   */
  reported_at: string;
  /**
   * Recorded changes waiting to publish.
   */
  pending: number;
  /**
   * Changes the platform rejected, kept until retried or discarded in SQL.
   */
  failed: number;
  /**
   * When the oldest waiting change was recorded.
   */
  oldest_pending_at?: string;
  /**
   * When a runtime last published changes.
   */
  last_published_at?: string;
  /**
   * The code of the latest rejected change.
   */
  last_failure_code?: string;
  /**
   * retrying when waiting changes are held back by publications that keep failing.
   */
  publishing: 'ok' | 'retrying';
  /**
   * When capture last left a change out because the backlog was full; those changes were not recorded.
   */
  last_dropped_at?: string;
};

