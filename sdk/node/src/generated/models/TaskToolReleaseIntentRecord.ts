/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolReleaseIntent } from './TaskToolReleaseIntent.js';
/**
 * An owner-approved source and native app review. Approval covers only this immutable intent; every execution capability remains unavailable in this milestone.
 */
export type TaskToolReleaseIntentRecord = {
  version: 1;
  id: string;
  account_id: string;
  requester_key_id: string;
  intent_sha256: string;
  intent: TaskToolReleaseIntent;
  status: 'pending' | 'approved' | 'rejected' | 'revoked' | 'expired' | 'stale';
  created_at: string;
  expires_at: string;
  decided_at?: string;
  bindings_verified: boolean;
  ready_to_apply: boolean;
};

