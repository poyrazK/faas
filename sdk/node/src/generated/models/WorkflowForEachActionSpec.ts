/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowOutboundSpec } from './WorkflowOutboundSpec.js';
import type { WorkflowRetrySpec } from './WorkflowRetrySpec.js';
/**
 * Exactly one of run, path or outbound. No nested iteration, dependencies,
 * guards or exception routes. Omitted input sends the item itself. Explicit
 * input templates read input.item, input.index, input.input (original run input)
 * and outputs of the parent's declared dependencies. Inputs are never re-rendered
 * on retry. Mutating outbound retries require provider idempotency support.
 *
 */
export type WorkflowForEachActionSpec = {
  run?: string;
  path?: string;
  outbound?: WorkflowOutboundSpec;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  /**
   * Typed JSON input template for each item.
   */
  input?: (Record<string, any> | string | number | boolean | null);
  /**
   * Per-item action timeout, bounded by the workflow plan limit.
   */
  timeout?: string;
  retry?: WorkflowRetrySpec;
};

