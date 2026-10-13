/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryExecution } from './EventRecoveryExecution.js';
import type { WorkflowInitialEvidenceRepairReceipt } from './WorkflowInitialEvidenceRepairReceipt.js';
export type WorkflowInitialEvidenceDiagnosis = {
  review_id: string;
  job_id?: string;
  run_id: string;
  resume_count: number;
  observed_at: string;
  reason: string;
  repairable: boolean;
  execution?: EventRecoveryExecution;
  repair?: WorkflowInitialEvidenceRepairReceipt;
};

