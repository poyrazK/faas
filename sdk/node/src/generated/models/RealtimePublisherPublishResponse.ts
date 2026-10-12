/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Publisher Publish Response.
 */
export type RealtimePublisherPublishResponse = {
  sequence: string;
  durable: boolean;
  replayed: boolean;
  /**
   * Immediate live fanout may be incomplete; retained delivery is committed.
   */
  partial: boolean;
};

