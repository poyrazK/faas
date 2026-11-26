/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Expected scalar at a JSON Pointer in a sandbox response.
 */
export type APISandboxJSONAssertion = {
  /**
   * JSON Pointer into the response. An empty string selects the response root.
   */
  pointer: string;
  /**
   * Expected JSON scalar value, including null.
   */
  equals: string | number | boolean | null;
};

