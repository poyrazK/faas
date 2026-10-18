/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioObjectsObject } from './ScenarioObjectsObject.js';
/**
 * Successful mutation completion receipt; repeated writes remain separate entries.
 */
export type ScenarioObjectsMutation = {
  /**
   * Logical participant responsible for this mutation when managed work is enrolled.
   */
  workload?: string;
  /**
   * Mutation completion order within the namespace.
   */
  sequence: number;
  method: 'PUT' | 'DELETE';
  key: string;
  status: 200 | 204;
  object?: ScenarioObjectsObject;
};

