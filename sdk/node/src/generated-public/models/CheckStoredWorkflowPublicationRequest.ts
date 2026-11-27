/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowCheckExclusion } from './StoredWorkflowCheckExclusion.js';
import type { StoredWorkflowPublishCheckScenario } from './StoredWorkflowPublishCheckScenario.js';
/**
 * Run assertions and coverage on the saved draft on the server; mocks remain hypothetical. At most 24 MiB total and 3 MiB per simulation. Complete, valid traces and passing assertions are required. Coverage policy overrides require_coverage=false.
 */
export type CheckStoredWorkflowPublicationRequest = {
  expected_version: number;
  require_coverage?: boolean;
  scenarios: Array<StoredWorkflowPublishCheckScenario>;
  exclusions: Array<StoredWorkflowCheckExclusion>;
};

