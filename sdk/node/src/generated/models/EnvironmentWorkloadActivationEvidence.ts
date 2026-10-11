/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Evidence summary for the exact current reviewed workload graph; retained workloads count only when their exact live deployment remains in the active release set. This evidence does not authorize candidate activation or serving.
 */
export type EnvironmentWorkloadActivationEvidence = {
  graph_id: string;
  source_id: string;
  environment_id: string;
  revision_id: string;
  definition_digest: string;
  generation: number;
  intent_version: number;
  plan_hash: string;
  graph_phase: string;
  graph_error_code?: string;
  artifacts_prepared: boolean;
  candidates: number;
  /**
   * Unchanged graph members verified against their exact live active release-set deployments.
   */
  retained_workloads_recorded: number;
  captures_recorded: number;
  guest_config_acknowledgements_recorded: number;
  restores_recorded: number;
  smokes_recorded: number;
  job_smokes_recorded: number;
  framework_ready_acknowledgements_recorded: number;
  qualified: boolean;
  activated: boolean;
  serving: boolean;
  blocking_reasons: Array<string>;
};

