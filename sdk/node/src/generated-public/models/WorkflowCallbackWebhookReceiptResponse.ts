/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Acknowledgement after a verified webhook completes or cannot change a callback.
 */
export type WorkflowCallbackWebhookReceiptResponse = {
  callback_id: string;
  status: 'received' | 'ignored';
  duplicate: boolean;
};

