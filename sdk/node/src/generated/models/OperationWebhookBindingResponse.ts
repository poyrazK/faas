/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubjectSpec } from './OperationSubjectSpec.js';
import type { OperationWebhookSignal } from './OperationWebhookSignal.js';
/**
 * Bound operation, owner, mode, event selection and current endpoint binding revision.
 */
export type OperationWebhookBindingResponse = {
  endpoint_id: string;
  app_id: string;
  scope: string;
  operation: string;
  platform_tenant_id: string;
  event_types: Array<string>;
  mode: 'start' | 'signal';
  subject?: OperationSubjectSpec;
  signals?: Array<OperationWebhookSignal>;
  version: number;
  created_at: string;
  updated_at: string;
};

