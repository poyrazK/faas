/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit provider endpoint declared safe to probe using managed outbound admission. Queries, redirects and unsuccessful expected statuses are unsupported.
 */
export type OutboundBindingProbePolicy = {
  method: 'GET' | 'HEAD';
  path: string;
  expected_status: number;
};

