/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-recipient queue outcome; admission does not imply client receipt and idempotent replays return the original response.
 */
export type ManagedRealtimePublishResponse = {
  /**
   * Number of live subscriber output queues or retained-resume wake-ups that accepted delivery work.
   */
  queued: number;
  /**
   * Live and resumable subscribers targeted across reachable nodes.
   */
  subscribers?: number;
  /**
   * Subscribers whose bounded output queues were full.
   */
  queue_full?: number;
  /**
   * Target subscribers that could not be queued for another per-connection reason.
   */
  failed?: number;
  /**
   * Whether a node or any target subscriber did not accept the publish.
   */
  partial?: boolean;
  /**
   * Active realtime nodes that accepted the publish request.
   */
  nodes_queried?: number;
  /**
   * Active realtime nodes that did not accept the publish request.
   */
  nodes_unavailable?: number;
  /**
   * Committed channel sequence; present when durable is true.
   */
  sequence?: number;
  /**
   * Whether this publish was committed to retained channel history before fan-out.
   */
  durable?: boolean;
};
