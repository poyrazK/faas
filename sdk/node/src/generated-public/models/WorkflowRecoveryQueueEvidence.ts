/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowRetryEvidenceDiagnosis } from './WorkflowRetryEvidenceDiagnosis.js';
/**
 * Read-only exact-generation diagnosis for a returned retry queue entry; never a worker evaluation or admission grant. Parent repairability also requires the exact live generation to remain retained. Omitted for initial recoveries, which use their separate evidence endpoints.
 */
export type WorkflowRecoveryQueueEvidence = {
  diagnosis: WorkflowRetryEvidenceDiagnosis;
  evidence_url: string;
  cli_command: string;
};

