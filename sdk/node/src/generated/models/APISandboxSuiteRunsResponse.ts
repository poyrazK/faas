/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxReleaseGate } from './APISandboxReleaseGate.js';
import type { APISandboxSuiteRunSummary } from './APISandboxSuiteRunSummary.js';
export type APISandboxSuiteRunsResponse = {
  /**
   * Latest automatic launch status for this candidate when the app has requested an automatic run.
   */
  automatic_status?: 'queued' | 'waiting_for_capacity' | 'waiting_for_baseline' | 'waiting_for_scenarios' | 'unavailable' | 'started' | 'approval_ended' | 'disabled';
  release_gate?: APISandboxReleaseGate;
  runs: Array<APISandboxSuiteRunSummary>;
};

