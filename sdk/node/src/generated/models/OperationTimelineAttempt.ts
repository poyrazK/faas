/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationAttemptLogs } from './OperationAttemptLogs.js';
/**
 * One HTTP execution or workflow step attempt as read; a running attempt is not re-emitted when it ends.
 */
export type OperationTimelineAttempt = {
  /**
   * Execution generation of an HTTP attempt. Step attempts omit it because recovery resumes the same workflow run.
   */
  generation?: number;
  execution_id: string;
  step?: string;
  attempt: number;
  outcome: 'running' | 'succeeded' | 'retry' | 'failed' | 'dead_letter' | 'cancelled' | 'unknown';
  http_status?: number;
  error?: string;
  started_at: string;
  finished_at?: string;
  next_attempt_at?: string;
  logs?: OperationAttemptLogs;
};

