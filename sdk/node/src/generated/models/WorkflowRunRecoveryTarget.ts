/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One reviewed workflow run and resume count. Retry children also identify the saved parent item.
 */
export type WorkflowRunRecoveryTarget = {
  run_id: string;
  expected_resume_count: number;
  /**
   * Required only with parent_job_id. Unique position of the queued parent item whose saved failed outcome matches this run and resume count.
   */
  parent_position?: number;
};

