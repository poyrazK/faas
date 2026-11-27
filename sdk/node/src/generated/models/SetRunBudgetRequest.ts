/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunBudgetLimits } from './RunBudgetLimits.js';
/**
 * Replacement budget policy and the revision required to update it.
 */
export type SetRunBudgetRequest = {
  expected_revision: number;
  enabled: boolean;
  limits: RunBudgetLimits;
};

