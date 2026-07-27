/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Cron creation payload: schedule expression and target URL.
 */
export type CreateCronRequest = {
  app_id: string;
  schedule: string;
  path?: string;
  enabled?: boolean | null;
};

