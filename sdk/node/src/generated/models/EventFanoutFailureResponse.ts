/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A terminal subscription routing failure before an invocation was created.
 */
export type EventFanoutFailureResponse = {
  event_id: string;
  event_source: string;
  event_type: string;
  subscription_id: string;
  state: 'failed';
  attempts: number;
  last_error: string;
  /**
   * When the event was accepted.
   */
  created_at: string;
  /**
   * When recipient routing became terminal.
   */
  failed_at: string;
};

