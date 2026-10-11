/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckAttempt } from './AutomationCheckAttempt.js';
/**
 * Requires at least one assertion. Loop item assertions use step <loop>/action, loop name and canonical zero-based item index.
 */
export type AutomationCheckExpectation = {
  step: string;
  loop?: string;
  item_index?: number;
  state?: 'mocked' | 'would_execute' | 'resolved' | 'expanded' | 'would_wait' | 'would_retry' | 'timed_out' | 'failed' | 'dead' | 'skipped' | 'blocked' | 'error';
  reason?: string;
  when_matched?: boolean;
  /**
   * Exact JSON assertion; explicit null differs from omitted.
   */
  output?: any;
  attempt_count?: number;
  attempts?: Array<AutomationCheckAttempt>;
};

