/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { BulkIssueLifecycleAction } from './BulkIssueLifecycleAction.js';
/**
 * Bounded issue selection and one lifecycle or assignment operation to apply atomically.
 */
export type BulkIssueActionRequest = {
  issue_ids: Array<string>;
  action: BulkIssueLifecycleAction;
};

