/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveSuiteObservation } from './AutomationLiveSuiteObservation.js';
/**
 * A missing side means the case was not selected in that suite. A selected case without a run has a not_started observation.
 */
export type AutomationLiveSuiteCaseComparison = {
  name: string;
  /**
   * Equal hashes and current terminal reports are required for regression, recovery or unchanged. Hash mismatch is incompatible even when a report is pending; absent evidence is unknown. Added and removed take precedence when only one suite selected the case.
   */
  change: 'regression' | 'recovery' | 'unchanged' | 'added' | 'removed' | 'incompatible' | 'unknown';
  baseline?: AutomationLiveSuiteObservation;
  candidate?: AutomationLiveSuiteObservation;
};

