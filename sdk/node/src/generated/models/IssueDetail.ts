/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Issue } from './Issue.js';
import type { IssueActivity } from './IssueActivity.js';
import type { IssueImpact } from './IssueImpact.js';
import type { IssueOccurrence } from './IssueOccurrence.js';
import type { IssueRelease } from './IssueRelease.js';
/**
 * Issue metadata and independently paginated occurrence, release, and activity collections.
 */
export type IssueDetail = {
  issue: Issue;
  events: Array<IssueOccurrence>;
  releases: Array<IssueRelease>;
  activity: Array<IssueActivity>;
  impact: IssueImpact;
  next_event_cursor?: string;
  next_release_cursor?: string;
  next_activity_cursor?: string;
};

