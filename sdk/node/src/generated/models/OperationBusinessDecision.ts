/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-reported business decision with a public explanation and optional exact policy rule identity.
 */
export type OperationBusinessDecision = {
  workflow: string;
  /**
   * UTF-8 byte limit; nonempty text without control characters.
   */
  instance_id: string;
  code: string;
  /**
   * Public decision explanation; UTF-8 byte limit; nonempty text without control characters.
   */
  description: string;
  rule_id: string;
  /**
   * Exact policy rule version; UTF-8 byte limit; nonempty text without control characters.
   */
  rule_version: string;
};

