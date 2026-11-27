/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Version-fenced PagerDuty primary receiver configuration with write-only credentials.
 */
export type SetMCPReadinessPagerDutyRequest = {
  /**
   * Current native PagerDuty policy version; zero is valid for first configuration.
   */
  expected_version: number;
  /**
   * Select PagerDuty primary for incidents not yet attempted; false restores webhook primary.
   */
  enabled: boolean;
  /**
   * Events API v2 key for the selected region; required when enabling and omitted when disabling.
   */
  integration_key?: string;
  /**
   * Events API region for this replacement configuration; defaults to US when omitted. Omit when disabling.
   */
  region?: 'us' | 'eu';
};

