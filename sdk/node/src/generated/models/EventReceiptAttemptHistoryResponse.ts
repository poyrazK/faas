/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InvocationAttemptResponse } from './InvocationAttemptResponse.js';
export type EventReceiptAttemptHistoryResponse = {
  event_source: string;
  event_id: string;
  subscription_id: string;
  original_invocation_id: string;
  /**
   * History is not backfilled and may be pruned; absence is not proof of no delivery.
   */
  coverage: 'recorded_attempts_only';
  attempts: Array<InvocationAttemptResponse>;
  /**
   * Opaque cursor for the next older page.
   */
  next_after?: string;
};

