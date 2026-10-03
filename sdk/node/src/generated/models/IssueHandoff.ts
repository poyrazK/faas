/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Issue } from './Issue.js';
import type { IssueActivity } from './IssueActivity.js';
import type { IssueEvent } from './IssueEvent.js';
import type { IssueHandoffEvidence } from './IssueHandoffEvidence.js';
import type { IssueHandoffRequest } from './IssueHandoffRequest.js';
import type { IssueImpact } from './IssueImpact.js';
import type { IssueRelease } from './IssueRelease.js';
/**
 * Opt-in issue.handoff webhook data, captured atomically with a created,
 * regressed, reopened, or impact_threshold_reached transition. Retries
 * preserve the snapshot. The 128 KiB packet includes retained sanitized
 * evidence; gaps explicitly describe unavailable or truncated evidence.
 * The legacy JSON envelope carries this under payload.data; CloudEvents
 * carries it under data. Receivers must authenticate separately to follow
 * the evidence API paths. Empty webhook filters do not select this event.
 *
 */
export type IssueHandoff = {
  schema_version: 1;
  /**
   * Stable handoff identity shared by every recipient; distinct from transition and delivery IDs.
   */
  id: string;
  type: 'issue.handoff';
  generated_at: string;
  issue: Issue;
  transition: IssueActivity;
  sample?: IssueEvent;
  release?: IssueRelease;
  request?: IssueHandoffRequest;
  impact: IssueImpact;
  evidence: IssueHandoffEvidence;
  gaps: Array<string>;
};

