/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Payload-free coalesced outcome for one deferred delivery attempt.
 */
export type EventAcknowledgmentOutcome = {
  /**
   * Delivery whose attempt produced this outcome.
   */
  invocation_id: string;
  /**
   * Fenced invocation attempt number.
   */
  attempt: number;
  /**
   * Replay generation fenced by this outcome.
   */
  replay_generation: number;
  /**
   * Deferred acknowledgment operation or scheduler expiry outcome.
   */
  kind: 'accepted' | 'renewed' | 'renewal_rejected' | 'completed' | 'expired' | 'retry_requested' | 'dead_letter_requested';
  /**
   * Database time of the first retained occurrence.
   */
  first_at: string;
  /**
   * Database time of the latest retained occurrence.
   */
  last_at: string;
  /**
   * Coalesced count saturating at the documented maximum.
   */
  occurrences: number;
  /**
   * Fixed history expiry anchored to the attempt deadline.
   */
  retain_until: string;
};

