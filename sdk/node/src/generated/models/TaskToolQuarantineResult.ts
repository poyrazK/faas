/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Historical observation that the exact recorded candidate worker IDs were fenced from new claims. No cross-database atomic commit, active-lease cancellation, worker shutdown or current-health certification is implied.
 */
export type TaskToolQuarantineResult = {
  operation_id: string;
  handover_id: string;
  qualification_sha256: string;
  worker_deployment_id: string;
  serving_deployment_id: string;
  worker_ids: Array<string>;
  evidence_sha256: string;
  observed_at: string;
  recorded_at: string;
};

