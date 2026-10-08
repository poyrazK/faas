/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryHistoryEntry } from './EventRecoveryHistoryEntry.js';
export type EventRecoveryHistory = {
  job_id: string;
  entries: Array<EventRecoveryHistoryEntry>;
  /**
   * Exclusive last ID for the next page; omitted on the final page.
   */
  next_after?: number;
};

