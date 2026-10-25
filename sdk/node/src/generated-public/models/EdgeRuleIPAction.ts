/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * CIDR allow/deny evaluator. Deny is checked AFTER allow so a single-IP deny wins.
 */
export type EdgeRuleIPAction = {
  allow?: Array<string>;
  deny?: Array<string>;
};

