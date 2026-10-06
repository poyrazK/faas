/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Register an immutable database source bound to a managed app operation policy and routing contract.
 */
export type CreateCommitSourceRequest = {
  name: string;
  /**
   * Active queue policy containing this application. Scope must be account or platform_tenant according to allow_tenant_selection. Environment-scoped policies are unsupported.
   */
  operation_policy: string;
  /**
   * Immutable contract. Version 1 uses one source lane; version 2 requires explicit event routing and a business key.
   */
  contract_version?: 1 | 2;
  /**
   * Immutable owner grant authorizing this trusted database to select active customers linked to the fixed target application. Requires contract version 2 and a platform_tenant queue policy.
   */
  allow_tenant_selection?: boolean;
};

