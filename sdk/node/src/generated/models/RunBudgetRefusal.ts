/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RunBudgetKind } from './RunBudgetKind.js';
export type RunBudgetRefusal = {
  kind: RunBudgetKind;
  ref: string;
  metric: 'enabled' | 'concurrent' | 'invocations' | 'compute_mb_ms';
};

