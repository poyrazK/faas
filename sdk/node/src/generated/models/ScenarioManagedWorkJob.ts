/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioManagedWorkAttempt } from './ScenarioManagedWorkAttempt.js';
/**
 * One managed async invocation. Runtime identities support inspection; comparison retains request digest and retry outcomes.
 */
export type ScenarioManagedWorkJob = {
  id: string;
  workload: string;
  app_id: string;
  deployment_id: string;
  method: string;
  path: string;
  payload_sha256: string;
  state: string;
  attempts: Array<ScenarioManagedWorkAttempt>;
};

