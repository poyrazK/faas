/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Policy selection impact for an intersecting captured operation, including any preserved public exception.
 */
export type RoutePolicyAffectedOperation = {
  method: string;
  path: string;
  relation: string;
  public?: boolean;
  before_rule_id?: string;
  after_rule_id?: string;
};

