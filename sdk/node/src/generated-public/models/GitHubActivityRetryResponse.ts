/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer-safe confirmation that recent failed GitHub activity was queued.
 */
export type GitHubActivityRetryResponse = {
  ok: boolean;
  retried_webhooks: number;
  retried_checks: number;
  status: string;
};

