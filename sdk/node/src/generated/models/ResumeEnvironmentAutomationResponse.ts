/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ResumeEnvironmentAutomationResponse = {
  /**
   * Stage whose paused automation was enabled.
   */
  environment: string;
  /**
   * Number of crons enabled.
   */
  crons_resumed: number;
  /**
   * Crons enabled by this call.
   */
  cron_ids: Array<string>;
};

