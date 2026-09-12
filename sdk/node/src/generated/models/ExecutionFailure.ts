/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Caller-safe terminal failure classification.
 */
export type ExecutionFailure = {
  /**
   * Stable caller-safe failure vocabulary.
   */
  code: string;
  /**
   * Host paths and VM command lines are never exposed.
   */
  message: string;
};

