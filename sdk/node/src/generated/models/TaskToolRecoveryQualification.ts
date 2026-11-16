/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolReleaseApp } from './TaskToolReleaseApp.js';
/**
 * Owner attestation of fresh local checks, exact recovery target and a locked native snapshot. The fixed deadline is observed_at plus 60 seconds, independently of the original release approval. Qualifying or approving never renews it.
 */
export type TaskToolRecoveryQualification = {
  handover_id: string;
  operation_id: string;
  revision: number;
  action: 'restore';
  target_deployment_id: string;
  expected_serving_deployment_id: string;
  bindings_sha256: string;
  evidence_sha256: string;
  native_apps: Array<TaskToolReleaseApp>;
  observed_at: string;
  expires_at: string;
};

