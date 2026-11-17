/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { HostedTaskSourceReview } from './HostedTaskSourceReview.js';
/**
 * Immutable source intent metadata. Not an owner decision or execution plan. Expired ciphertext cannot be renewed under the same identity.
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
   * False because retained source alone has no owner execution authorization.
   */
  ready_to_apply: boolean;
  /**
   * False because storing authored Task source does not verify runtime credential bindings.
   */
  bindings_verified: boolean;
  /**
   * True because a separate owner decision is required before any retained Task source can execute.
   */
  owner_authorization_required: boolean;
};

