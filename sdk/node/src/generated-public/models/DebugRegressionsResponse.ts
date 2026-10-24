/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DebugRegressionItem } from './DebugRegressionItem.js';
/**
 * Response from GET /v1/apps/{slug}/debug/regressions (ADR-127 / PR-B).
 * `since` echoes the effective window applied.
 *
 */
export type DebugRegressionsResponse = {
  since: string;
  regressions: Array<DebugRegressionItem>;
};

