/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type EventSchemaRolloutRequest = {
  /**
   * Concrete event source; wildcard patterns are not allowed.
   */
  source: string;
  /**
   * Concrete event type; wildcard patterns are not allowed.
   */
  type: string;
  version: string;
  /**
   * Proposed Draft 2020-12 JSON Schema, at most 64 KiB; external references are forbidden. Omit to use the registered version. It is never registered by this request.
   */
  schema?: any;
  /**
   * Event data values to validate, at most 64 KiB each. No event envelopes are required.
   */
  samples?: Array<any>;
  /**
   * Optional inclusive platform acceptance time; requires until.
   */
  from?: string;
  /**
   * Optional exclusive acceptance time; requires from. Cut off at observed_at when in the future.
   */
  until?: string;
  /**
   * Requires a retained range when nonzero. Zero or omitted checks up to 100 matching payloads when a range is supplied.
   */
  retained_limit?: number;
};

