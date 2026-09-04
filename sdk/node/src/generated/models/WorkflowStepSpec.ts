/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRetrySpec } from './WorkflowRetrySpec.js';
/**
 * One workflow step. The canonical ADR-081 target is `run`; `path`
 * and `method` remain accepted for the existing HTTP wake executor
 * during the runtime migration. Exactly one of `run`, `path`, or
 * `wait_for_event` must be supplied.
 *
 */
export type WorkflowStepSpec = {
  name: string;
  /**
   * Named platform operation to invoke.
   */
  run?: string;
  /**
   * JSON input passed to the named operation.
   */
  input?: (Record<string, any> | string | number | boolean | null);
  /**
   * HTTP wake path, retained for compatibility with the existing executor.
   */
  path?: string;
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS';
  depends_on?: Array<string>;
  wait_for_event?: string;
  /**
   * Step or wait timeout in time.ParseDuration form, for example `30s`; workflow also accepts fixed 24-hour day suffixes such as `7d`.
   */
  timeout?: string;
  on_timeout?: string;
  retry?: (WorkflowRetrySpec | null);
};

