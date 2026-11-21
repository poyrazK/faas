/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagExperimentGuardrailRequest } from './FlagExperimentGuardrailRequest.js';
/**
 * Changes the default guardrails; both are on unless turned off.
 */
export type FlagExperimentGuardrailsRequest = {
  server_error_rate?: FlagExperimentGuardrailRequest;
  p95_latency?: FlagExperimentGuardrailRequest;
};

