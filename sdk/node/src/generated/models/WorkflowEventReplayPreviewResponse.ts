/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReplayPreviewRetention } from './EventReplayPreviewRetention.js';
import type { WorkflowEventReplayPreviewMatch } from './WorkflowEventReplayPreviewMatch.js';
/**
 * Read-only, bounded preview of captured workflow event recipients; potential admissions do not guarantee future workflow admission.
 */
export type WorkflowEventReplayPreviewResponse = {
  app_slug: string;
  workflow_name: string;
  from: string;
  until: string;
  /**
   * Acceptance-time upper bound carried forward from the first page.
   */
  cutoff_at: string;
  /**
   * Server timestamp when this workflow preview page was assembled.
   */
  observed_at: string;
  coverage: 'retained_envelopes';
  retention: EventReplayPreviewRetention;
  /**
   * Retained envelopes examined on this page.
   */
  scanned_count: number;
  /**
   * Envelopes whose original snapshot contains the named app workflow.
   */
  captured_count: number;
  /**
   * Captured recipients whose immutable trigger filter matches the event.
   */
  matched_count: number;
  filter_mismatch_count: number;
  /**
   * Envelopes with a captured snapshot that excludes this workflow recipient.
   */
  not_captured_count: number;
  /**
   * Legacy envelopes without an original recipient snapshot.
   */
  unknown_recipient_count: number;
  /**
   * Matching recipients with a durable admission receipt; these are deduplicated.
   */
  already_admitted_count: number;
  /**
   * Matching recipients without an admission receipt; not a guarantee of future quota or target eligibility.
   */
  potential_admission_count: number;
  matches: Array<WorkflowEventReplayPreviewMatch>;
  /**
   * Continue even if this page has no matches; absent when no more currently retained envelopes follow.
   */
  next_after?: string;
};

