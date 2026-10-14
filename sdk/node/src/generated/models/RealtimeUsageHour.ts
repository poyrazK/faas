/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime usage in one UTC hour.
 */
export type RealtimeUsageHour = {
  /**
   * Start of the UTC hour.
   */
  hour: string;
  /**
   * Most connections open at once in any minute of the hour.
   */
  peak_connections: number;
  /**
   * Connections held multiplied by seconds.
   */
  connection_seconds: number;
  /**
   * Messages sent to clients
   */
  delivered_messages: number;
  /**
   * Bytes of delivered messages.
   */
  delivered_bytes: number;
  /**
   * Client messages forwarded to the app.
   */
  received_messages: number;
  /**
   * Bytes of received client messages.
   */
  received_bytes: number;
};

