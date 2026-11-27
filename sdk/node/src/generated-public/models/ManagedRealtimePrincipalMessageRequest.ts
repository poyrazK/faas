/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Principal message payload with optional retained notification and push fallback settings.
 */
export type ManagedRealtimePrincipalMessageRequest = {
  principal: string;
  /**
   * Standard base64; at most 4096 decoded bytes.
   */
  data_base64: string;
  binary?: boolean;
  delivery?: 'live';
  message_id?: string;
  request_receipt?: boolean;
};

