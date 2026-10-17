/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioPostgresSpec } from './ScenarioPostgresSpec.js';
/**
 * Sanitized fixture SQL and its exact collection contract for a fresh contained workload.
 */
export type CreateScenarioPostgresRequest = {
  app_slug: string;
  spec: ScenarioPostgresSpec;
  /**
   * Sanitized UTF-8 fixture SQL; the server also enforces a 65536-byte limit.
   */
  sql: string;
};

