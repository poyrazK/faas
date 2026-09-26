/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowConditionSpec } from './WorkflowConditionSpec.js';
import type { WorkflowRetrySpec } from './WorkflowRetrySpec.js';
/**
 * One workflow step. The canonical ADR-081 target is `run`; `path`
 * and `method` remain accepted for the existing HTTP wake executor
 * during the runtime migration. Exactly one of `run`, `path`,
 * `wait_for_event`, `wait_for_callback`, `wait_for_duration`, or
 * `wait_for_condition` must be supplied.
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
   * Park for one account-authorized callback completion. Requires a wait timeout.
   */
  wait_for_callback?: boolean;
  /**
   * Durable timer, from 1s up to the plan's 7-day workflow wait limit. Fixed day suffixes such as `3d` mean 24-hour days; no compute is held while waiting.
   */
  wait_for_duration?: string;
  wait_for_condition?: WorkflowConditionSpec;
  /**
   * Step or wait timeout in time.ParseDuration form, for example `30s`; workflow also accepts fixed 24-hour day suffixes such as `7d`.
   */
  timeout?: string;
  on_timeout?: string;
  retry?: (WorkflowRetrySpec | null);
};

