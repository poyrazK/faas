/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bind a verified provider endpoint to a published automation with revision control.
 */
export type PutWebhookAutomationBindingRequest = {
  expected_version: number;
  workflow_name: string;
  event_type: string;
  filter?: Record<string, any>;
  take_over_delivery: boolean;
};

