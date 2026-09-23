/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Durable receipt for an accepted outbound webhook delivery.
 */
export type DeliverAppEventResponse = {
  id: string;
  webhook_id: string;
  destination: string;
  event: string;
  status: 'pending';
  status_url: string;
};

