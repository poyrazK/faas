/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationLiveSuiteMember } from './AutomationLiveSuiteMember.js';
/**
 * At most 16 KiB. Freeze an ordered saved-case selection and attach existing verification run IDs. No assertions, verdicts, keys or payloads are accepted. Saving never executes or checks a run. Existing run links cannot be replaced or removed.
 */
export type SaveAutomationLiveSuiteRequest = {
  expected_version: number;
  expected_published_version: number;
  expected_deployment_id: string;
  cases: Array<AutomationLiveSuiteMember>;
};

