/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Public condition vocabulary declared on a pinned workflow milestone step. A definition does not evaluate a predicate.
 */
export type OperationBusinessConditionDefinition = {
  /**
   * Optional states where this workflow step is expected to report the condition. Names must belong to its declared workflow states. Omission declares no expectation; this never gates transitions or evaluates business truth.
   */
  expected_states?: Array<string>;
  /**
   * Optional public team or actor responsible for refreshing this condition report. UTF-8 byte bound; no controls or surrounding whitespace. Omission is unassigned.
   */
  refresh_owner?: string;
  /**
   * Optional public application-declared refresh instructions. UTF-8 byte bound; no controls or surrounding whitespace. Displayed as text and never executed.
   */
  refresh_instructions?: string;
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

