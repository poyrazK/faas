/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Instance log window of an HTTP attempt. Lines other requests wrote on the same instance during the window interleave.
 */
export type OperationAttemptLogs = {
  instance_id: string;
  since: string;
  until?: string;
  archive_dates: Array<string>;
  correlation: 'instance_window';
};

