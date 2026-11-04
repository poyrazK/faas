/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppWebhookDeliveryResponse } from './AppWebhookDeliveryResponse.js';
/**
 * Response from POST /v1/apps/{slug}/webhooks/{id}/deliveries/{did}/retry.
 * Mirrors AppWebhookDeliveryResponse; the row in `delivery` is
 * re-emitted with status='pending' and next_attempt_at=now().
 *
 */
export type AppWebhookRetryDeliveryResponse = {
  delivery: AppWebhookDeliveryResponse;
};

