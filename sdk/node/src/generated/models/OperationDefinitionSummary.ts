/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubjectSpec } from './OperationSubjectSpec.js';
import type { OperationWorkflowStep } from './OperationWorkflowStep.js';
/**
 * Immutable definition metadata without bundled schema documents.
 */
export type OperationDefinitionSummary = {
  /**
   * Declared milestone names; schema documents are available on the full definition.
   */
  milestones?: Array<string>;
  /**
   * Resolved app-declared read-only workflow steps mapped to this definition's transaction-backed milestones.
   */
  workflow_steps?: Array<OperationWorkflowStep>;
  subject?: OperationSubjectSpec;
  /**
   * Opt-in customer-owned HTTP transaction protocol. Absence means ordinary HTTP execution.
   */
  http_transaction_version?: 1;
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

