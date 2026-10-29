/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedOperationEffect } from './ManagedOperationEffect.js';
/**
 * Opt-in managed HTTP operation handler response; encode as JSON and return from the handler after requiring X-Gregale-Operation-Result-Version header value 1. The entire response is bounded to 1 MiB. Ordinary invocation responses are unchanged. Only supported on managed request operations, including Commit operations.
 */
export type ManagedOperationResult = {
  gregale_operation_result: 1;
  /**
   * Business result persisted atomically with effect enqueue; any JSON value, including null.
   */
  result: any;
  effects: Array<ManagedOperationEffect>;
};

