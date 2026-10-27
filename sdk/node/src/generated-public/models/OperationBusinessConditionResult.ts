/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Application-reported condition result matching the pinned milestone declaration. Omitted conditions remain unreported; unknown is explicit. No execution authority or causal inference.
 */
export type OperationBusinessConditionResult = {
  id: string;
  /**
   * Exact public description from the pinned milestone declaration; UTF-8 byte bound.
   */
  description: string;
  result: 'pass' | 'fail' | 'unknown';
};

