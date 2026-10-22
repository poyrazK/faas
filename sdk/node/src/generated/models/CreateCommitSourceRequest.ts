/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Register an immutable database source bound to managed Operations or application-event publication. Event destinations require version 3, no operation policy and no customer selection.
 */
export type CreateCommitSourceRequest = {
  name: string;
  /**
   * Required for operation destinations and forbidden for event destinations. Active queue policy containing this application. Scope must be account or platform_tenant according to allow_tenant_selection. Environment-scoped policies are unsupported.
   */
  operation_policy?: string;
  /**
   * Immutable contract. Version 1 uses one source lane; version 2 requires explicit operation routing; version 3 requires an event destination.
   */
  contract_version?: 1 | 2 | 3;
  /**
   * Immutable owner grant authorizing this trusted database to select active customers linked to the fixed target application. Requires contract version 2 and a platform_tenant queue policy.
   */
  allow_tenant_selection?: boolean;
  /**
   * Immutable destination. Event publishes under the bound application source instead of invoking that application.
   */
  destination?: 'operation' | 'event';
};

