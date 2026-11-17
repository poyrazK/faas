/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One finite exact nonnegative counter sample with its original scrape timestamp. A cumulative current value alone is not recent activity. The integer ceiling is wire safety, not an admission quota.
 */
export type ObsRuntimeAppTaskOperationPoint = {
  value: number;
  observed_at: string;
};

