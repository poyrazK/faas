/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Read-only preview or cancellation result. Only eligible generations can be cancelled. Repeating a cancellation against the same cancelled generation does not append another history entry.
 */
export type WorkflowRecoveryCancellationResponse = {
  job_id: string;
  position: number;
  run_id?: string;
  resume_count: number;
  status?: string;
  outcome: 'not_admitted' | 'changed' | 'unknown' | 'operation_owned' | 'already_cancelled' | 'already_terminal' | 'eligible' | 'cancelled';
  preview: boolean;
};

