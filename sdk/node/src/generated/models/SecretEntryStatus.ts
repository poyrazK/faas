/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Whether the owner has set the requested secret. Never carries the value.
 */
export type SecretEntryStatus = {
  status: 'pending' | 'set';
  app: string;
  key: string;
  scope: string;
  expires_at: string;
};

