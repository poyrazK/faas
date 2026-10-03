/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One bounded latency or synthetic server-error fault for matching internal service calls.
 */
export type ScenarioTestChaosRule = {
  from?: string;
  to: string;
  kind: 'latency' | 'http_status';
  percent: number;
  latency_ms?: number;
  status_code?: number;
  seed: number;
};

