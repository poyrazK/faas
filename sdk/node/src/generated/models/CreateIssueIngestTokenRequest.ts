/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Create a short-lived reporting credential for one existing deployment and environment.
 */
export type CreateIssueIngestTokenRequest = {
  deployment_id: string;
  environment?: string;
  name: string;
  expires_at: string;
};

