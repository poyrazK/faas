/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact action and original actor filters applied before control-receipt pagination.
 */
export type EventExecutionLineageRetryJobControlFilters = {
  action?: '' | 'pause' | 'resume' | 'cancel';
  /**
   * Exact actor UUID in hyphenated form or store for an internal caller. Empty means no filter.
   */
  actor_id?: string;
  credential_type?: '' | 'session' | 'api_key' | 'deploy_token' | 'platform_tenant_token' | 'internal';
};

