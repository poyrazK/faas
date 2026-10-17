/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed scheduling intent for one HTTP cron; command and execution data are excluded.
 */
export type DeploymentCronConfig = {
  id: string;
  schedule: string;
  path: string;
  enabled: boolean;
  timezone: string;
  skip_if_running: boolean;
};

