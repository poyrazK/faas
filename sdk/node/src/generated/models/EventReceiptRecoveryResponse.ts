/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReceiptExecutionResponse } from './EventReceiptExecutionResponse.js';
/**
 * Latest retained generic replay and history; original execution remains separate. Completed means this latest replay recovered; counts may decrease as execution records expire. Absence does not prove that no historical replay occurred.
 */
export type EventReceiptRecoveryResponse = {
  retained_replay_count: number;
  latest_replay: EventReceiptExecutionResponse;
  /**
   * Account-authenticated paginated replay history for this recipient.
   */
  history_url: string;
};

