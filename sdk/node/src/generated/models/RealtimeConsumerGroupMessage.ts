/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Realtime Consumer Group Message.
 */
export type RealtimeConsumerGroupMessage = {
  sequence: string;
  data_base64: string;
  binary: boolean;
  metadata?: Record<string, string>;
  target_message_id?: string;
  version: number;
  event: string;
  deleted: boolean;
  created_at: string;
};

