/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Issue an access token for an app. expires_in_hours defaults to 168 (7 days). integration_ids narrows the token to listed AI integrations bound to the app; empty allows every AI integration bound to the app and the account's routes.
 */
export type CreateOutboundAIAccessTokenRequest = {
  name: string;
  expires_in_hours?: number;
  integration_ids?: Array<string>;
};

