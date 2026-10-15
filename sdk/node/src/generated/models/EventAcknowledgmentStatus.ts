/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained invocation and processing lease observation without secret token material.
 */
export type EventAcknowledgmentStatus = {
  /**
   * Durable invocation receiving the deferred processing callback.
   */
  invocation_id: string;
  /**
   * Current execution claim number for this processing observation; zero follows an in-place replay reset.
   */
  attempt: number;
  /**
   * Current in-place replay generation fencing old delivery attempts.
   */
  replay_generation: number;
  /**
   * Underlying durable invocation lifecycle state.
   */
  state: string;
  /**
   * Processing phase derived from the retained claim and current database clock.
   */
  processing_state: 'waiting' | 'delivering' | 'processing' | 'expired' | 'completed' | 'failed' | 'dead_letter' | 'cancelled' | 'superseded';
  /**
   * Current invocation lease expiry; absent after expiry recovery.
   */
  lease_expires_at?: string;
  /**
   * Absolute one-hour processing deadline of the matching attempt.
   */
  deadline_at?: string;
};

