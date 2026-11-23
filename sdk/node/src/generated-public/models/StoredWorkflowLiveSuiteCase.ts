/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowLiveVerification } from './StoredWorkflowLiveVerification.js';
/**
 * A server-derived case observation; unavailable records keep their original run identity.
 */
export type StoredWorkflowLiveSuiteCase = {
  name: string;
  run_id?: string;
  outcome: 'not_started' | 'passed' | 'failed' | 'pending' | 'outdated' | 'unavailable';
  verification?: StoredWorkflowLiveVerification;
};

