/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ExecutionFile } from './ExecutionFile.js';
import type { ExecutionLimitRequest } from './ExecutionLimitRequest.js';
import type { ExecutionNetworkPolicy } from './ExecutionNetworkPolicy.js';
/**
 * Source and JSON input for one disposable execution. Send either the
 * legacy `source` string or an ephemeral `files` bundle with an
 * `entrypoint`. v1 supports only the listed interpreter runtimes and
 * `network.mode=none`; dependencies, secrets, environment injection, and
 * persistent disks are not part of this contract.
 *
 */
export type CreateExecutionRequest = {
  runtime: 'node22' | 'node24' | 'python312' | 'python313';
  /**
   * Single-file source code; never returned by execution reads.
   */
  source?: string;
  /**
   * Normalized relative path of the file to execute when `files` is supplied.
   */
  entrypoint?: string;
  /**
   * Regular files staged into the guest's ephemeral scratch filesystem.
   */
  files?: Array<ExecutionFile>;
  /**
   * One complete JSON value delivered to the guest as input.
   */
  input?: any;
  limits?: ExecutionLimitRequest;
  network?: ExecutionNetworkPolicy;
};

