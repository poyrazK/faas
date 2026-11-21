/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner-only plan metadata. A target identity reserves future allocation and creates no resources.
 */
export type HostedTaskRehearsalPlan = {
  version: 1;
  account_id: string;
  intent_id: string;
  intent_sha256: string;
  review_sha256: string;
  binding_sha256: string;
  target_id: string;
  queue_namespace: string;
  plan_sha256: string;
  status: 'planned' | 'approved' | 'claimed' | 'revoked' | 'stale' | 'expired';
  created_at: string;
  binding_checked_at: string;
  expires_at: string;
  approved_at?: string;
  claimed_at?: string;
  no_traffic: boolean;
};

