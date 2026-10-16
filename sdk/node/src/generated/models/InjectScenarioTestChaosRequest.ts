/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioTestChaosRule } from './ScenarioTestChaosRule.js';
/**
 * Bounded HTTP, private-service DNS, and TCP fault plan applied only within one scenario test namespace.
 */
export type InjectScenarioTestChaosRequest = {
  duration_ms: number;
  rules: Array<ScenarioTestChaosRule>;
};

