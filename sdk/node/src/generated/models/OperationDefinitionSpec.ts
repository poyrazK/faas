/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OperationSubjectSpec } from './OperationSubjectSpec.js';
import type { OperationWorkflowStep } from './OperationWorkflowStep.js';
/**
 * Resolved immutable contract for one HTTP handler. Ownership comes from verified authentication, never input fields. Production admission stays disabled until the HTTP execution adapter is qualified.
 */
export type OperationDefinitionSpec = {
  /**
   * Declared public milestone names mapped to bundled JSON Schemas. Requires http_transaction_version 1. At most 16 names, with an aggregate 16384 schema bytes.
   */
  milestones?: Record<string, any>;
  /**
   * Materialized workflow step mappings included in this compact definition view.
   */
  workflow_steps?: Array<OperationWorkflowStep>;
  subject?: OperationSubjectSpec;
  name: string;
  method: 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  path: string;
  owner: 'platform_tenant';
  /**
   * Bundled JSON Schema 2020-12; only local document references are supported.
   */
  input_schema: any;
  /**
   * Bundled schema for the business result selected by the execution target.
   */
  output_schema: any;
  progress_stages: Array<string>;
  completion_webhook_id?: string;
  recovery?: 'reconcile_on_unknown' | 'safe_retry';
  /**
   * Opt into the internal customer Operation PostgreSQL receipt protocol. Version 1 saves and replays the full JSON business result; it does not negotiate managed operation envelopes or named effects.
   */
  http_transaction_version?: 1;
};

