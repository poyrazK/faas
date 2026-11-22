/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowTestCase } from './StoredWorkflowTestCase.js';
import type { StoredWorkflowTestInputs } from './StoredWorkflowTestInputs.js';
/**
 * At most 32 uniquely named cases; sample up to 3 MiB, cases up to 24 MiB combined, suite up to 27 MiB. Data is explicitly persisted, never captured from real runs.
 */
export type StoredWorkflowTestSuite = {
  sample: StoredWorkflowTestInputs;
  cases: Array<StoredWorkflowTestCase>;
};

