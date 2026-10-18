/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveVerification } from './AutomationLiveVerification.js';
/**
 * A server-derived case observation; unavailable records keep their original run identity.
 */
export type AutomationLiveSuiteCase = {
  name: string;
  run_id?: string;
  outcome: 'not_started' | 'passed' | 'failed' | 'pending' | 'outdated' | 'unavailable';
  verification?: AutomationLiveVerification;
};

