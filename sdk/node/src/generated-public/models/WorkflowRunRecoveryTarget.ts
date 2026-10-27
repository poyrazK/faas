/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One reviewed workflow run and resume count. Retry children identify the saved parent item. Initial failure targets may identify retained generation-zero evidence, which is checked again at creation and admission.
 */
export type WorkflowRunRecoveryTarget = {
  /**
   * Optional captured initial failure identity. Requires expected_resume_count zero and no recovery parent. Must match retained app, workflow, run and failure status.
   */
  failure_evidence_id?: string;
  run_id: string;
  expected_resume_count: number;
  /**
   * Required only with parent_job_id. Unique position of the queued parent item whose saved failed outcome matches this run and resume count.
   */
  parent_position?: number;
};

