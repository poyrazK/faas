/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OrgAppDeploymentSummary } from './OrgAppDeploymentSummary.js';
/**
 * Newest-first paginated safe deployment history for one workspace app.
 */
export type OrgAppDeploymentListResponse = {
  items: Array<OrgAppDeploymentSummary>;
  /**
   * Cursor for the next older page; omitted when there are no more rows.
   */
  next_before?: string;
};

