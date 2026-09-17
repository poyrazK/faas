/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * RFC 8693 / OAuth 2.0 token endpoint error response.
 */
export type OAuthTokenExchangeError = {
  error: 'invalid_request' | 'invalid_grant' | 'invalid_target' | 'invalid_scope' | 'unsupported_subject_token_type' | 'unsupported_token_type' | 'temporarily_unavailable';
  error_description?: string;
};

