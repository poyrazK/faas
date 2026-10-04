/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optimistic stage promotion request; an expected-version conflict requires a fresh read.
 */
export type FlagRolloutPromotionRequest = {
  expected_version: number;
  rule_id: string;
};

