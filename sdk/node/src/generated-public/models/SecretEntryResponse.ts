/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Fifteen-minute entry link with separate private status material. Only entry_url and expires_at may be exposed to agents; the link works only in the owner's signed-in session.
 */
export type SecretEntryResponse = {
  entry_url: string;
  ticket: string;
  exchange_code: string;
  expires_at: string;
};

