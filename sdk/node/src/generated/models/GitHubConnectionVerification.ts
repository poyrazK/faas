/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Live GitHub installation permissions, repository selection and verification time.
 */
export type GitHubConnectionVerification = {
  account_type: string;
  repository_selection: string;
  permissions: Record<string, string>;
  verified_at: string;
  repository_count: number;
};

