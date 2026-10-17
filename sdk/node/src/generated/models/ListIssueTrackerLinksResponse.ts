/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueTrackerLink } from './IssueTrackerLink.js';
/**
 * Cursor-paginated tracker references attached to the selected issue group.
 */
export type ListIssueTrackerLinksResponse = {
  items: Array<IssueTrackerLink>;
  next_cursor?: string;
};

