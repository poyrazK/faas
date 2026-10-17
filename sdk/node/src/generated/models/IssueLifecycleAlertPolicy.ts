/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Native new-issue/regression environment filter and shared per-group cooldown.
 */
export type IssueLifecycleAlertPolicy = {
  revision: number;
  /**
   * Environments permitted by the saved policy; an empty array means all.
   */
  environments: Array<string>;
  cooldown_seconds: number;
};

