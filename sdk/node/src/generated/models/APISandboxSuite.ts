/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type APISandboxSuite = {
  revision: string;
  /**
   * Automatically queue an advisory suite when an eligible build reaches approval.
   */
  run_before_approval?: boolean;
  scenario_ids: Array<string>;
};

