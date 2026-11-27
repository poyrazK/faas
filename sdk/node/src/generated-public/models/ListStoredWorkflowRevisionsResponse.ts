/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { StoredWorkflowRevisionResponse } from './StoredWorkflowRevisionResponse.js';
/**
 * Newest-first immutable publication history and pagination metadata.
 */
export type ListStoredWorkflowRevisionsResponse = {
  revisions: Array<StoredWorkflowRevisionResponse>;
  total: number;
  limit: number;
  offset: number;
};

