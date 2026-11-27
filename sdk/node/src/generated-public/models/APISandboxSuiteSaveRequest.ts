/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ordered scenario selection and revision precondition for saving a suite.
 */
export type APISandboxSuiteSaveRequest = {
  /**
   * Use the current suite revision or the empty string to create the suite.
   */
  expected_revision: string;
  /**
   * Enable or disable automatic advisory suites. An omitted value keeps the existing setting and an empty scenario list disables it.
   */
  run_before_approval?: boolean;
  scenario_ids: Array<string>;
};

