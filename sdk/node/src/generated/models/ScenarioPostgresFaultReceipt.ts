/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioPostgresFaultSpec } from './ScenarioPostgresFaultSpec.js';
/**
 * Durable original-owner confirmation of a database-specific connection fence and independent recovery probe. Missing proof remains inconclusive; a proven recovery exceeding the budget fails comparison.
 */
export type ScenarioPostgresFaultReceipt = {
  id: string;
  account_id: string;
  run_id: string;
  app_id: string;
  resource_id: string;
  deployment_id: string;
  instance_id: string;
  recorder_session_id: string;
  invocation_id: string;
  checkpoint_attempt_id: string;
  owner_id: string;
  backend_sha256: string;
  plan_sha256: string;
  spec: ScenarioPostgresFaultSpec;
  state: 'requested' | 'cutting' | 'active' | 'recovering' | 'recovered' | 'failed';
  activated: boolean;
  recovered: boolean;
  closed_connections: number;
  /**
   * Confirmed active interval from disconnect acknowledgement until restoration starts.
   */
  outage_ms: number;
  /**
   * Elapsed time from planned restoration through a fresh connection and privilege probe.
   */
  recovery_ms: number;
  error?: string;
};

