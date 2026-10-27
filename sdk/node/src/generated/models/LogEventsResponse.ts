/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { LogQueryEvent } from './LogQueryEvent.js';
/**
 * One newest-first page of an app's log ledger.
 */
export type LogEventsResponse = {
  /**
   * Effective window start after clamping to retention.
   */
  since: string;
  until: string;
  events: Array<LogQueryEvent>;
  /**
   * Present when more events match; pass it back as cursor.
   */
  next_cursor?: string;
};

