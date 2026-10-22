/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRoutingRecoveryExpectation } from './EventRoutingRecoveryExpectation.js';
/**
 * Identity of one terminal fanout failure to replay. Use either acceptance and generation guards or recovery_expectation; combining the guard formats is invalid.
 */
export type ReplayEventFanoutFailureRequest = {
  /**
   * Optional routing generation. Requires expected_accepted_at; checked with failed state under the recipient row lock. A changed generation returns 409 event_replay_generation_changed without mutation.
   */
  expected_generation?: number;
  /**
   * Optional exact original acceptance instant. Checked under the receipt row lock in the replay transaction; replacement returns 409 without mutation. Guarded replay targets the original acceptance snapshot, not backfills.
   */
  expected_accepted_at?: string;
  recovery_expectation?: EventRoutingRecoveryExpectation;
  /**
   * Allow expired in the replay event fanout failure request: explicitly override delivery age for this replay generation or historical backfill job. Preserves deterministic invocation identity and manual controls.
   */
  allow_expired?: boolean;
  event_id: string;
  event_source: string;
  subscription_id: string;
};

