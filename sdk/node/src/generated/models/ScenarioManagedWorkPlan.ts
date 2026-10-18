/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ScenarioManagedWorkParticipant } from './ScenarioManagedWorkParticipant.js';
import type { ScenarioPostgresFaultSpec } from './ScenarioPostgresFaultSpec.js';
import type { ScenarioWorkerTermination } from './ScenarioWorkerTermination.js';
/**
 * Explicit participants and fixture permissions for managed async-route rehearsals. Requires contained commands and simulation; rollout is operator gated.
 */
export type ScenarioManagedWorkPlan = {
  version: 1;
  primary: string;
  worker_termination?: ScenarioWorkerTermination;
  postgres_fault?: ScenarioPostgresFaultSpec;
  participants: Array<ScenarioManagedWorkParticipant>;
};

