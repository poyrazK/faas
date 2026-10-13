/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Exact equality against sanitized stored evidence; every populated field must match. At least one nonempty selector is required. Empty fields are wildcards.
 */
export type IssueSplitMatcher = {
  exception_type?: string;
  error_code?: string;
  route?: string;
  source_kind?: '' | 'exception' | 'http' | 'runtime' | 'worker';
};

