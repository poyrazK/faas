/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventAcknowledgmentOutcome } from './EventAcknowledgmentOutcome.js';
/**
 * Most recently updated retained outcome groups for a scoped consumer.
 */
export type EventAcknowledgmentHistory = {
  /**
   * Outcome groups ordered by latest occurrence descending with stable tie breakers.
   */
  outcomes: Array<EventAcknowledgmentOutcome>;
  /**
   * More retained outcome groups existed at the read snapshot.
   */
  truncated: boolean;
  /**
   * Diagnostic retained invocation coverage since the ledger rollout.
   */
  coverage: 'retained_coalesced_deferred_outcomes';
};

