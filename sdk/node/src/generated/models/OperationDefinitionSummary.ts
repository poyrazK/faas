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
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  owner: 'platform_tenant';
  completion_webhook_id?: string;
  recovery: 'reconcile_on_unknown' | 'safe_retry';
  progress_stages: Array<string>;
  created_at: string;
};

