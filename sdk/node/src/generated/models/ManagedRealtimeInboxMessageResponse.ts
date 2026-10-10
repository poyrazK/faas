/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Retained principal inbox event with payload and mutation metadata.
 */
export type ManagedRealtimeInboxMessageResponse = {
  target_message_id?: string;
  version: number;
  event: string;
  deleted: boolean;
  message_id: string;
  sequence: number;
  data_base64: string;
  binary: boolean;
  created_at: string;
  expires_at: string;
};

