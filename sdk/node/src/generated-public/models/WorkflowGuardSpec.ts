/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded declarative when predicate. Specify exactly one of all, any,
 * not, or ref/op/value. References select input or a direct dependency
 * output using input.foo or steps.lookup.output.body.foo without template
 * delimiters. Equality is type-sensitive and accepts scalar literals only.
 * Numeric comparisons are exact; numbers are bounded to 4096 bytes and
 * exponent magnitude 4096. Missing paths fail comparisons, including ne;
 * exists distinguishes missing from present null. not negates normally.
 * At most 32 predicate nodes, 8 levels and 16 KiB per guard. Guards are
 * forbidden on on_failure/on_timeout handler targets. A false guard skips
 * its step and propagates through dependencies; skipped paths do not join.
 *
 */
export type WorkflowGuardSpec = {
  all?: Array<WorkflowGuardSpec>;
  any?: Array<WorkflowGuardSpec>;
  not?: WorkflowGuardSpec;
  /**
   * Input path or output path of a declared direct dependency.
   */
  ref?: string;
  op?: 'eq' | 'ne' | 'gt' | 'gte' | 'lt' | 'lte' | 'exists';
  /**
   * Scalar JSON literal; numeric operators require a number and exists requires a boolean.
   */
  value?: (string | number | boolean | null);
};

