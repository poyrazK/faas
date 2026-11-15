/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner approval of the exact saved source-definition review, or a bounded retry of the already approved backfill and refresh workflow.
 */
export type DataPipelineSchemaApprovalRequest = {
  expected_revision: number;
  change_id: string;
  retry?: boolean;
};

