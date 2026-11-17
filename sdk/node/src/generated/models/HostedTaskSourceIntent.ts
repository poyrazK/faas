/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskSourceReview } from './HostedTaskSourceReview.js';
/**
 * Immutable hosted source identity and last observed owner review metadata. Source review approval does not grant credential or execution authority. Expired ciphertext cannot be renewed under the same identity.
 */
export type HostedTaskSourceIntent = {
  version: 1;
  id: string;
  account_id: string;
  requester_key_id: string;
  intent_sha256: string;
  review_sha256: string;
  review: HostedTaskSourceReview;
  created_at: string;
  expires_at: string;
  source_state: 'retained' | 'expired';
  /**
   * Last recorded hosted source review state; absent on older receipts means pending until source expiry.
   */
  review_status?: 'pending' | 'approved' | 'rejected' | 'revoked' | 'stale' | 'expired';
  /**
   * Original hosted source owner decision time, retained unchanged by exact repeats.
   */
  decided_at?: string;
  /**
   * False because approval of a retained hosted source review is not permission to execute it.
   */
  ready_to_apply: boolean;
  /**
   * False because storing authored Task source does not verify runtime credential bindings.
   */
  bindings_verified: boolean;
  /**
   * True until separate owner-controlled execution authorization and runtime credential qualification exist.
   */
  owner_authorization_required: boolean;
};

