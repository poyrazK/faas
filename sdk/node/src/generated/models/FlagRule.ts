/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Ordered customer targeting rule; supplied constraints combine with AND. Boolean flags require a boolean value; variant flags may omit value to use weighted assignment.
 */
export type FlagRule = {
  id: string;
  customers?: Array<string>;
  /**
   * Owner-managed customer group key.
   */
  group?: string;
  /**
   * Basis points of eligible customers; omitted means all eligible customers.
   */
  rollout?: number;
  value?: (boolean | string);
};

