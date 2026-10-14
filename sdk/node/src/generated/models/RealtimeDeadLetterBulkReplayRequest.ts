/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeDeadLetterReplaySelection } from './RealtimeDeadLetterReplaySelection.js';
/**
 * Realtime Dead Letter Bulk Replay Request.
 */
export type RealtimeDeadLetterBulkReplayRequest = {
  group_id: string;
  /**
   * Unique sequences selected explicitly from a preview.
   */
  items: Array<RealtimeDeadLetterReplaySelection>;
};

