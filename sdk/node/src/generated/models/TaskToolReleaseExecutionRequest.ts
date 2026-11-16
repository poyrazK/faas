/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolReleaseReceipt } from './TaskToolReleaseReceipt.js';
/**
 * Exact local execution identity and bounded transition. Private claim material is submitted by the trusted CLI and is never returned in API receipts.
 */
export type TaskToolReleaseExecutionRequest = {
  action: 'authorize' | 'claim' | 'guard' | 'reconcile' | 'complete' | 'executor_exited';
  fence_version?: 1;
  operation_id: string;
  bindings_sha256: string;
  claim_token?: string;
  stage?: 'prepared' | 'deploying_worker' | 'deploying_web' | 'replacements_started' | 'web_promoted' | 'draining_previous' | 'previous_parked' | 'complete';
  receipt?: TaskToolReleaseReceipt;
};

