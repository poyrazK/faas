/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Draft definition and the revision that the caller edited.
 */
export type SaveAutomationDraftRequest = {
  /**
   * Saved revision observed by the editor; zero creates the first draft.
   */
  expected_version: number;
  definition: WorkflowSpec;
};

