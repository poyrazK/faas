/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunBudgetAlert } from './RunBudgetAlert.js';
import type { RunBudgetKind } from './RunBudgetKind.js';
import type { RunBudgetLimits } from './RunBudgetLimits.js';
import type { RunBudgetUsage } from './RunBudgetUsage.js';
/**
 * Current revision, limits and usage for one budget.
 */
export type RunBudgetPolicy = {
  kind: RunBudgetKind;
  ref: string;
  revision: number;
  enabled: boolean;
  limits: RunBudgetLimits;
  usage: RunBudgetUsage;
  alerts: Array<RunBudgetAlert>;
};

