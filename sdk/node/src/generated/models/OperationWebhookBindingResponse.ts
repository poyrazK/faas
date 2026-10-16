/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bound operation, owner, event selection and current endpoint binding revision.
 */
export type OperationWebhookBindingResponse = {
  endpoint_id: string;
  app_id: string;
  scope: string;
  operation: string;
  platform_tenant_id: string;
  event_types: Array<string>;
  version: number;
  created_at: string;
  updated_at: string;
};

