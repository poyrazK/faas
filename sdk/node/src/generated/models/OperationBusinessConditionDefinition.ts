/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a predicate.
 */
export type OperationBusinessConditionDefinition = {
  /**
   * Optional pinned maximum condition report age in seconds. Zero or omission disables the budget; this measures reporting freshness without changing the application result.
   */
  evidence_max_age_seconds?: number;
  id: string;
  /**
   * Public UTF-8 explanation without control characters; byte bound.
   */
  description: string;
};

