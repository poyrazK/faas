/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppWebhookDeliveryAttemptResponse } from './AppWebhookDeliveryAttemptResponse.js';
/**
 * A newest-first page of attempts for one account-owned delivery.
 */
export type AppWebhookDeliveryAttemptListResponse = {
  attempts: Array<AppWebhookDeliveryAttemptResponse>;
  /**
   * Cursor for the next page.
   */
  next_token?: string;
};

