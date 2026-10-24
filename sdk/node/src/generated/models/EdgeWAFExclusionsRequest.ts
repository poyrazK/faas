/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * CRS rule IDs to add to and remove from a kind=waf rule's exclude_rule_ids (ADR-831 amendment 13).
 */
export type EdgeWAFExclusionsRequest = {
  add?: Array<number>;
  remove?: Array<number>;
};

