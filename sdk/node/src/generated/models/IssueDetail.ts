/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Issue } from './Issue.js';
import type { IssueActivity } from './IssueActivity.js';
import type { IssueImpact } from './IssueImpact.js';
import type { IssueOccurrence } from './IssueOccurrence.js';
import type { IssueRelease } from './IssueRelease.js';
import type { IssueSplitRule } from './IssueSplitRule.js';
/**
 * Canonical issue metadata and independently paginated combined history. Linked IDs resolve to their current canonical issue; original identities and counters appear in group_members.
 */
export type IssueDetail = {
  /**
   * Routing rules associated with the displayed original identities or derived branches; lower priority wins per source.
   */
  split_rules?: Array<IssueSplitRule>;
  issue: Issue;
  /**
   * Original fingerprint identities with their own lifetime counters.
   */
  group_members?: Array<Issue>;
  events: Array<IssueOccurrence>;
  releases: Array<IssueRelease>;
  activity: Array<IssueActivity>;
  impact: IssueImpact;
  next_event_cursor?: string;
  next_release_cursor?: string;
  next_activity_cursor?: string;
};

