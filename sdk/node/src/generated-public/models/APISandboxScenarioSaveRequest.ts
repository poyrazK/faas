/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxScenarioInput } from './APISandboxScenarioInput.js';
export type APISandboxScenarioSaveRequest = (APISandboxScenarioInput & {
  /**
   * Private retained request provenance from a draft for this release.
   */
  source_request_id?: string;
  /**
   * Required with source_request_id. Use the draft revision, or the empty string if the release had no scenario. A changed revision returns 409.
   */
  expected_revision?: string;
});

