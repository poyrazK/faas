/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Mirror quality gate for a canary stage.
 */
export type MirrorCleanCondition = {
  /**
   * Minimum completed mirror comparisons required in the window.
   */
  min_invocations: number;
  /**
   * Trailing mirror-summary window in seconds.
   */
  window_s: number;
};

