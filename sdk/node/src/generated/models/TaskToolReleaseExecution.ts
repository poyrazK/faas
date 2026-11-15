/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolReleaseReceipt } from './TaskToolReleaseReceipt.js';
/**
 * Single-use owner authorization bound to a private local binding digest and an immutable operation. Existing claims are never automatically taken over.
 */
export type TaskToolReleaseExecution = {
  operation_id: string;
  bindings_sha256: string;
  authorizer_key_id: string;
  state: 'authorized' | 'running' | 'reconcile_required' | 'completed';
  authorized_at: string;
  claim_sha256?: string;
  stage?: 'prepared' | 'deploying_worker' | 'deploying_web' | 'replacements_started' | 'web_promoted' | 'draining_previous' | 'previous_parked' | 'complete';
  receipt?: TaskToolReleaseReceipt;
  completed_stages?: Array<'baseline_captured' | 'worker_submitted' | 'web_submitted' | 'workers_recorded' | 'promotion_recorded' | 'previous_workers_parked' | 'release_completed'>;
};

