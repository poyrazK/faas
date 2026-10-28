/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRetrySpec } from './WorkflowRetrySpec.js';
/**
 * A code-first workflow's entry handler. Exactly one of run or path. Gregale POSTs the run's journal to it at every durable boundary; the SDKs (@gregale/sdk-node/workflows, faas_sdk.workflows) answer with the operations the code reaches next. A code workflow has an empty steps list.
 */
export type WorkflowCodeSpec = {
  /**
   * Handler name; the entry path is /<run>.
   */
  run?: string;
  /**
   * Explicit entry path starting with '/'.
   */
  path?: string;
  /**
   * Per-call timeout as a duration string; at most the plan's step timeout. Default 30s.
   */
  timeout?: string;
  retry?: WorkflowRetrySpec;
};

