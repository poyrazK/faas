/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeDeadLetterReplayOutcome } from './RealtimeDeadLetterReplayOutcome.js';
export type RealtimeDeadLetterBulkReplayResult = {
  group_id: string;
  replayed: number;
  /**
   * One outcome per selection, in request order.
   */
  outcomes: Array<RealtimeDeadLetterReplayOutcome>;
};

