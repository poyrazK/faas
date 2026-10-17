/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bind a verified provider endpoint to a customer operation with revision control (ADR-1193).
 */
export type PutOperationWebhookBindingRequest = {
  expected_version: number;
  /**
   * Environment scope; defaults to default.
   */
  scope?: string;
  /**
   * Operation name resolved on the scope's live deployment for each event.
   */
  operation: string;
  /**
   * Operation owner fixed by this binding.
   */
  platform_tenant_id: string;
  /**
   * Exact event types to admit; empty admits every verified event.
   */
  event_types?: Array<string>;
  take_over_delivery: boolean;
};

