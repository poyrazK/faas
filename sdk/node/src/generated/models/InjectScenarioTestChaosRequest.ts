/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioTestChaosRule } from './ScenarioTestChaosRule.js';
/**
 * Bounded fault plan applied only to service calls within one scenario test namespace.
 */
export type InjectScenarioTestChaosRequest = {
  duration_ms: number;
  rules: Array<ScenarioTestChaosRule>;
};

