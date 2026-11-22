/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Captured stored workflow name, event selection and current endpoint binding revision.
 */
export type WebhookWorkflowBindingResponse = {
  endpoint_id: string;
  workflow_name: string;
  event_type: string;
  filter: Record<string, any>;
  version: number;
  updated_at: string;
};

