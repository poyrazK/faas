/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppWebhookDeliveryResponse } from './AppWebhookDeliveryResponse.js';
/**
 * Paged deliveries surface for the dashboard's "recent
 * deliveries" pane. page_token is opaque + base64-encoded —
 * treat it as a cursor; do not parse it.
 *
 */
export type AppWebhookDeliveryListResponse = {
  deliveries: Array<AppWebhookDeliveryResponse>;
  /**
   * Cursor for the next page; empty/absent when the page is the last.
   */
  next_token?: string;
};

