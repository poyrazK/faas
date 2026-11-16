/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Native stored-policy concurrency condition for the exact canonical MCP resource, optionally including the converged account revision. It does not replace runtime authorization checks.
 */
export type MCPAccessPolicyExpectation = {
  resource: string;
  fingerprint: string;
  account_revision?: number;
};

