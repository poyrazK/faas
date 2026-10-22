/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A sanitized stack frame. Grouping excludes line numbers; in_app selects application evidence.
 */
export type IssueFrame = {
  file: string;
  function: string;
  line?: number;
  /**
   * One-based generated or original column; omitted when unknown. Excluded from grouping identity.
   */
  column?: number;
  in_app: boolean;
};

