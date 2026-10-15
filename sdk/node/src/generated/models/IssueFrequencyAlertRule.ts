/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type IssueFrequencyAlertRule = {
  key: string;
  enabled: boolean;
  /**
   * Exact issue environment. Empty string matches all environments.
   */
  environment: string;
  minimum_events: number;
  window_seconds: number;
  cooldown_seconds: number;
};

