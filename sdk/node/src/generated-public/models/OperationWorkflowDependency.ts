/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Direct prerequisite in the same application/customer/environment. References may have no retained state. Self references and duplicate targets are rejected.
 */
export type OperationWorkflowDependency = {
  subject_type: string;
  subject_id: string;
  workflow: string;
  instance_id: string;
  required_outcome_code?: string;
};

