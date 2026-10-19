/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * An issued access token. prefix is the token's first 12 characters, to recognise it.
 */
export type OutboundAIAccessToken = {
  id: string;
  name: string;
  app_id: string;
  prefix: string;
  integration_ids: Array<string>;
  expires_at: string;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
};

