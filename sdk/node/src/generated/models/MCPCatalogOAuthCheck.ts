/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public metadata compatibility result without source errors, provider response bodies or credentials.
 */
export type MCPCatalogOAuthCheck = {
  name: string;
  state: 'passed' | 'failed' | 'unverified';
  reason?: 'metadata_not_configured' | 'metadata_unavailable' | 'reviewed_endpoint_mismatch' | 'capability_not_advertised' | 'required_capability_not_supported' | 'scope_not_advertised';
};

