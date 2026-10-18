/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioPostgresTable } from './ScenarioPostgresTable.js';
/**
 * Versioned fixture digest, selected table contract and sampled lock-wait assertion.
 */
export type ScenarioPostgresSpec = {
  version: 1;
  fixture_sha256: string;
  tables: Array<ScenarioPostgresTable>;
  max_lock_wait_ms: number;
};

