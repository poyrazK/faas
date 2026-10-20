/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubjectSpec } from './OperationSubjectSpec.js';
import type { OperationWebhookSignal } from './OperationWebhookSignal.js';
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
   * Exact event types to admit; empty admits every verified event. Must be empty in signal mode.
   */
  event_types?: Array<string>;
  take_over_delivery: boolean;
  /**
   * start admits a new operation per event; signal resumes waiting operations (ADR-20261016100929).
   */
  mode?: 'start' | 'signal';
  /**
   * Signal mode only. The subject type the operation declares and a JSON Pointer into the provider event payload.
   */
  subject?: OperationSubjectSpec;
  /**
   * Signal mode only. Provider event types to deliver and the operation event each becomes; other types are acknowledged as ignored.
   */
  signals?: Array<OperationWebhookSignal>;
};

