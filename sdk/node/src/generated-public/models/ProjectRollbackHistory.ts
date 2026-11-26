/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectRollbackSummary } from './ProjectRollbackSummary.js';
/**
 * Project rollback receipts ordered with any running operation first.
 */
export type ProjectRollbackHistory = {
  project: string;
  project_id: string;
  operations: Array<ProjectRollbackSummary>;
};

