/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Issue } from './Issue.js';
/**
 * A bounded page of grouped issues with an opaque continuation cursor.
 */
export type ListIssuesResponse = {
  items: Array<Issue>;
  next_cursor?: string;
};

