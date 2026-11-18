/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical observation of the exact native park transition, drained instances and absent or expired candidate Task registrations. No active Task cancellation or current service-health certification is implied.
 */
export type TaskToolRetirementResult = {
  operation_id: string;
  handover_id: string;
  qualification_sha256: string;
  worker_deployment_id: string;
  serving_deployment_id: string;
  park_transition_id: string;
  worker_ids: Array<string>;
  evidence_sha256: string;
  observed_at: string;
  recorded_at: string;
};

