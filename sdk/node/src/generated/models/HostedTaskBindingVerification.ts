/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskBindingResult } from './HostedTaskBindingResult.js';
/**
 * Historical private-binding qualification metadata only. No credential values, references, source, database facts, owner decisions or execution authority.
 */
export type HostedTaskBindingVerification = {
  version: 1;
  account_id: string;
  intent_id: string;
  intent_sha256: string;
  review_sha256: string;
  binding_sha256: string;
  status: 'selected' | 'verifying' | 'passed' | 'failed' | 'stale' | 'revoked' | 'expired';
  checks: Array<HostedTaskBindingResult>;
  created_at: string;
  expires_at: string;
  checked_at?: string;
  valid_until?: string;
  ready_to_apply: boolean;
  bindings_verified: boolean;
  owner_authorization_required: boolean;
};

