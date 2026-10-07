/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable selected region, delivery purpose and original consent for durable dispatch.
 */
export type QueueGlobalQuotaDeliveryRequest = {
  region: string;
  purpose: 'grant' | 'retirement';
  runtime_consent_id?: string;
};

