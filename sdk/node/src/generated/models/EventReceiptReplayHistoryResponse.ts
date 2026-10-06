/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReceiptExecutionResponse } from './EventReceiptExecutionResponse.js';
/**
 * A page of retained generic replay executions for one captured event consumer.
 */
export type EventReceiptReplayHistoryResponse = {
  event_source: string;
  event_id: string;
  subscription_id: string;
  /**
   * Deterministic root delivery ID; its execution record may have expired.
   */
  original_invocation_id: string;
  replays: Array<EventReceiptExecutionResponse>;
  /**
   * Opaque cursor for the next older page; omit after when starting a fresh history read.
   */
  next_after?: string;
};

