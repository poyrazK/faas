/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRoutingRecoveryExpectation } from './EventRoutingRecoveryExpectation.js';
/**
 * Identity of one terminal fanout failure to replay.
 */
export type ReplayEventFanoutFailureRequest = {
  recovery_expectation?: EventRoutingRecoveryExpectation;
  /**
   * Allow expired in the replay event fanout failure request: explicitly override delivery age for this replay generation or historical backfill job. Preserves deterministic invocation identity and manual controls.
   */
  allow_expired?: boolean;
  event_id: string;
  event_source: string;
  subscription_id: string;
};

