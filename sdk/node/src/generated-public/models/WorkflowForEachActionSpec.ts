/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowGuardSpec } from './WorkflowGuardSpec.js';
import type { WorkflowOutboundSpec } from './WorkflowOutboundSpec.js';
import type { WorkflowRetrySpec } from './WorkflowRetrySpec.js';
/**
 * Exactly one of run, path or outbound. No nested iteration, dependencies,
 * waits, joins or exception routes. An optional when guard is evaluated once
 * for each item using input.item, input.index and input.input, plus outputs of
 * the parent's declared dependencies. Omitted input sends the item itself.
 * Explicit input templates use the same item context. Inputs and guard decisions
 * are snapshotted before dispatch and never reevaluated on retry. Mutating
 * outbound retries require provider idempotency support.
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
  when?: WorkflowGuardSpec;
};

