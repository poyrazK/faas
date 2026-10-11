/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioTestChaosMatch } from './ScenarioTestChaosMatch.js';
/**
 * Run-scoped counts of HTTP requests and TCP connections that matched the current or most recently cleared plan.
 */
export type ScenarioTestChaosMatchesResponse = {
  generation?: string;
  matches: Array<ScenarioTestChaosMatch>;
};

