/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxJSONAssertion } from './APISandboxJSONAssertion.js';
/**
 * Synthetic request and response expectations saved in a scenario.
 */
export type APISandboxExampleRequest = {
  title: string;
  method: 'GET' | 'HEAD' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /**
   * Same-host path and optional query.
   */
  path: string;
  /**
   * Optional JSON value for a request body. Limit of 2 KiB.
   */
  body?: any;
  expected_status: number;
  assertions?: Array<APISandboxJSONAssertion>;
};

