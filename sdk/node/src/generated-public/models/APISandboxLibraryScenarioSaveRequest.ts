/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxScenarioInput } from './APISandboxScenarioInput.js';
export type APISandboxLibraryScenarioSaveRequest = (APISandboxScenarioInput & {
  /**
   * Current revision for an edit, or the empty string for a new scenario.
   */
  expected_revision: string;
  /**
   * Private retained request identifier from a Debugger draft for this app. Accepted on creation.
   */
  source_request_id?: string;
});

