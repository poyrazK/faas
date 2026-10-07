/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalRuntimeConsentBindingResponse } from './GlobalRuntimeConsentBindingResponse.js';
/**
 * Retained asynchronous delivery state. Accepted means a verified receipt was stored; it never means admission activated or native capacity drained.
 */
export type GlobalQuotaDeliveryResponse = {
  id: string;
  global_app_id: string;
  allocation_id: string;
  issuer_id: string;
  generation: number;
  purpose: 'grant' | 'retirement';
  binding: GlobalRuntimeConsentBindingResponse;
  created_at: string;
  status: string;
  attempts: number;
  next_attempt_at: string;
  runtime_consent_id?: string;
  retirement_id?: string;
  claim_until?: string;
  accepted_at?: string;
  last_code?: string;
};

