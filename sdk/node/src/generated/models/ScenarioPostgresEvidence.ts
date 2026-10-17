/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioPostgresDelta } from './ScenarioPostgresDelta.js';
import type { ScenarioPostgresReceipt } from './ScenarioPostgresReceipt.js';
import type { ScenarioPostgresSnapshot } from './ScenarioPostgresSnapshot.js';
import type { ScenarioPostgresSpec } from './ScenarioPostgresSpec.js';
/**
 * Independent fixture observations with a fenced final capture and confirmed database/login deletion.
 */
export type ScenarioPostgresEvidence = (ScenarioPostgresReceipt & {
  status: 'complete';
  provenance: 'platform_postgres_collector';
  deployment_id: string;
  spec: ScenarioPostgresSpec;
  before: Array<ScenarioPostgresSnapshot>;
  after: Array<ScenarioPostgresSnapshot>;
  changes: Array<ScenarioPostgresDelta>;
  lock_samples: number;
  max_observed_lock_wait_ms: number;
  lock_budget_passed: boolean;
  cleanup_confirmed: boolean;
  reason?: string;
});

