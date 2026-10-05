/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectVersionLegalHold } from './ObjectVersionLegalHold.js';
import type { ObjectVersionRetention } from './ObjectVersionRetention.js';
/**
 * Durable public protection receipt containing immutable intent and bounded reconciliation progress.
 */
export type ObjectVersionProtection = {
  id: string;
  bucket_id: string;
  key: string;
  version_id: string;
  kind: 'retention' | 'legal_hold';
  state: 'waiting' | 'applying' | 'ready' | 'failed';
  retention?: ObjectVersionRetention;
  legal_hold?: ObjectVersionLegalHold;
  last_error_code?: 'provider_uncertain' | 'provider_unsupported' | 'provider_mismatch' | 'preparation_failed' | 'provider_rejected';
  created_at: string;
  updated_at: string;
};

