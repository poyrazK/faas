/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { WorkflowInitialEvidenceDiagnosis } from './WorkflowInitialEvidenceDiagnosis.js';
/**
 * Read-only initial recovery evidence. Prepared identifies a planned generation with no submitted recovery job and is never repairable; submitted uses exact-generation proof from the initial evidence endpoint. Neither stage grants execution authority.
 */
export type WorkflowRecoveryInitialQueueEvidence = {
  stage: 'prepared' | 'submitted';
  diagnosis: WorkflowInitialEvidenceDiagnosis;
  evidence_url: string;
  cli_command: string;
};

