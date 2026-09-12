/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionLimitRequest } from './ExecutionLimitRequest.js';
import type { ExecutionNetworkPolicy } from './ExecutionNetworkPolicy.js';
/**
 * Source and JSON input for one disposable execution. v1 supports only
 * the listed interpreter runtimes and `network.mode=none`; dependencies,
 * secrets, environment injection, and persistent disks are not part of
 * this contract.
 *
 */
export type CreateExecutionRequest = {
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  /**
   * Single-file source code; never returned by execution reads.
   */
  source: string;
  /**
   * One complete JSON value delivered to the guest as input.
   */
  input?: any;
  limits?: ExecutionLimitRequest;
  network?: ExecutionNetworkPolicy;
};

