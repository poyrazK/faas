/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Reporting credential metadata; the bearer secret appears only on initial creation.
 */
export type IssueIngestToken = {
  id: string;
  name: string;
  app_id: string;
  deployment_id: string;
  environment: string;
  expires_at: string;
  revoked_at?: string;
  token?: string;
};

