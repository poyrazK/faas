/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioSimulationResponse } from './ScenarioSimulationResponse.js';
/**
 * One exact workload, integration, method and path. Responses advance per idempotency key, or per fixture without a key; the final response repeats. Only named top-level JSON fields are retained.
 */
export type ScenarioSimulationFixture = {
  workload: string;
  name: string;
  integration: string;
  adapter: 'http' | 'resend';
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * Exact origin path without query, fragment, percent encoding or traversal. Resend supports POST /emails.
   */
  path: string;
  capture_fields?: Array<string>;
  responses: Array<ScenarioSimulationResponse>;
};

