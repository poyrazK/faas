/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit retirement recovery; allowing active grant revocation may invalidate a replacement connection.
 */
export type MCPOAuthRetirementAction = {
  action: 'retry' | 'acknowledge_manual';
  allow_active_grant_revocation?: boolean;
};

