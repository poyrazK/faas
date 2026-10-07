/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { GlobalRuntimeConsentBindingResponse } from './GlobalRuntimeConsentBindingResponse.js';
/**
 * Original directory consent and regional observation. Personal targets only; not runtime admission or billing authority.
 */
export type GlobalRuntimeConsentResponse = {
  id: string;
  issuer_id: string;
  global_app_id: string;
  owner_id: string;
  global_app_slug: string;
  binding: GlobalRuntimeConsentBindingResponse;
  expires_at: string;
  created_at: string;
  status: 'pending' | 'active' | 'revoking' | 'revoked' | 'expired';
  regional_created_at?: string;
  regional_revoked_at?: string;
};

