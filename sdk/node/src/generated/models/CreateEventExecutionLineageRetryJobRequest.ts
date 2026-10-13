/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventExecutionLineageRetrySelection } from './EventExecutionLineageRetrySelection.js';
export type CreateEventExecutionLineageRetryJobRequest = {
  request_id: string;
  /**
   * Unique outbox identities; server normalizes ascending order for idempotency.
   */
  cases: Array<EventExecutionLineageRetrySelection>;
};

