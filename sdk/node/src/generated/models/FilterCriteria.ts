/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FilterCriteriaClause } from './FilterCriteriaClause.js';
/**
 * FilterCriteria on a trigger. nil / omitted matches every record.
 * Top-level arrays combine via implicit OR for `$or` and
 * AND for `$and`; nested clauses honour the same shape.
 * Jsonpath implementation: github.com/PaesslerAG/jsonpath —
 * no eval semantics, no customer-supplied code execution.
 *
 */
export type FilterCriteria = {
  $or?: Array<FilterCriteriaClause>;
  $and?: Array<FilterCriteriaClause>;
  payload?: Array<FilterCriteriaClause>;
};

