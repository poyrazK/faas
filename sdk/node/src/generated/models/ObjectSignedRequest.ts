/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Temporary bearer capability for a direct provider request. Do not log or persist it.
 */
export type ObjectSignedRequest = {
  url: string;
  method: 'GET' | 'PUT';
  headers: Record<string, string>;
  expires_at: string;
};

