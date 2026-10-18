/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Separately gated warm rehearsal with one managed worker and one isolated database. Disconnects existing sessions and rejects new connections before restoring access. Excludes cold start, suspend/resume and worker termination.
 */
export type ScenarioPostgresFaultSpec = {
  workload: string;
  checkpoint: string;
  unavailable_ms: number;
  recover_within_ms: number;
};

