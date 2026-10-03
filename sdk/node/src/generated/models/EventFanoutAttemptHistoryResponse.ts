/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventFanoutAttemptResponse } from './EventFanoutAttemptResponse.js';
/**
 * Bounded immutable routing history for one app-scoped event identity.
 */
export type EventFanoutAttemptHistoryResponse = {
  app_slug: string;
  event_source: string;
  event_id: string;
  subscription_id?: string;
  history: Array<EventFanoutAttemptResponse>;
  /**
   * Opaque cursor bound to the app
   */
  next_before?: string;
};

