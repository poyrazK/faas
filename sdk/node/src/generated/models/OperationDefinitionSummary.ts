/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Immutable definition metadata without bundled schema documents.
 */
export type OperationDefinitionSummary = {
  id: string;
  app_id: string;
  scope: string;
  revision: string;
  deployment_id: string;
  release_id?: string;
  name: string;
  /**
   * Discovered account-owned single-task batch Job; accepted work retains its execution snapshot.
   */
  job?: string;
  /**
   * Workflow execution selected by this discoverable Operations definition.
   */
  workflow?: string;
  /**
   * Discovered customer transaction receipt contract for atomic HTTP completion.
   */
  transaction_receipt?: 'postgres_v1';
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  owner: 'platform_tenant';
  completion_webhook_id?: string;
  recovery: 'reconcile_on_unknown' | 'safe_retry';
  progress_stages: Array<string>;
  created_at: string;
};

