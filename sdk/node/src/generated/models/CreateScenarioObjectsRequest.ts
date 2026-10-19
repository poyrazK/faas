/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioObjectsData } from './ScenarioObjectsData.js';
import type { ScenarioObjectsSpec } from './ScenarioObjectsSpec.js';
/**
 * Fresh primary workload and exact initial objects for an isolated rehearsal namespace.
 */
export type CreateScenarioObjectsRequest = {
  app_slug: string;
  spec: ScenarioObjectsSpec;
  objects: Array<ScenarioObjectsData>;
};

