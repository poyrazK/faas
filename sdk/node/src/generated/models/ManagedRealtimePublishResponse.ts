/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Result of publishing a message to an endpoint-scoped channel.
 */
export type ManagedRealtimePublishResponse = {
  /**
   * Number of local owner queues that accepted the message.
   */
  queued: number;
  /**
   * Whether one or more active realtime nodes did not accept the publish.
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
};

