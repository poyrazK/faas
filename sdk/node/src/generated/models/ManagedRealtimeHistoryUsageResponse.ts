/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Account-scoped retained payload snapshot and plan cap; not a billable usage meter.
 */
export type ManagedRealtimeHistoryUsageResponse = {
  observed_at: string;
  /**
   * Endpoints with a retained channel head.
   */
  endpoint_count: number;
  /**
   * Retained channel heads, including empty heads.
   */
  channel_count: number;
  /**
   * Message rows still physically present, including expired rows awaiting cleanup.
   */
  stored_message_count: number;
  /**
   * Decoded payload bytes in physically present rows; excludes database overhead.
   */
  stored_payload_bytes: number;
  /**
   * Rows at or above each channel's current contiguous retention floor.
   */
  replayable_message_count: number;
  /**
   * Decoded payload bytes eligible for replay.
   */
  replayable_payload_bytes: number;
  /**
   * Plan-specific account cap for physically stored retained payload bytes.
   */
  payload_bytes_limit?: number;
  /**
   * Remaining account payload allowance; expired rows count until cleanup.
   */
  payload_bytes_remaining?: number;
};

