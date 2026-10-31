/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Passive deployment-attributed observations of the selected worker generation.
 */
export type WorkerRolloutObservation = {
  app_id: string;
  deployment_id: string;
  scope: string;
  project_id?: string;
  environment_id?: string;
  checked_at: string;
  basis: 'resident_generation_snapshot';
  selected_deployment_id?: string;
  running: number;
  starting: number;
  draining: number;
  other_residents: number;
  retired_residents: number;
  generation_converged: boolean;
  queue_processing_confirmed: boolean;
};

