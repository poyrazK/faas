/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Native branch join. Waits for all dependencies to finish and permits only
 * skips caused by false guards, including their descendants. Failed,
 * cancelled, unknown and exception-route skips cannot activate a join.
 * The first succeeded dependency in output_from order supplies the durable
 * output {source: step name, value: original output}. All inactive branches
 * skip the join and its continuation. Consumes zero execution attempts.
 * Requires 2-128 dependencies and cannot have input, method, when, timeout,
 * retry or exception routes, or be/depend on an exception handler.
 *
 */
export type WorkflowJoinSpec = {
  /**
   * Every direct dependency exactly once, in explicit selection priority order.
   */
  output_from: Array<string>;
};

