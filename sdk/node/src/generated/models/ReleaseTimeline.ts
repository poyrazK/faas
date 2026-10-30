/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseApprovalStatus } from './ReleaseApprovalStatus.js';
import type { ReleaseGateResult } from './ReleaseGateResult.js';
import type { ReleaseTimelineEvent } from './ReleaseTimelineEvent.js';
import type { ScheduledRelease } from './ScheduledRelease.js';
/**
 * One release's phase and what happened to it (ADR-20261022120744).
 */
export type ReleaseTimeline = {
  deployment_id: string;
  app: string;
  revision?: number;
  scope?: string;
  /**
   * The deployment's status, such as live or superseded.
   */
  status: string;
  /**
   * Where the release is, matched in this order.
   */
  phase: 'building' | 'failed' | 'cancelled' | 'rolled_back' | 'scheduled' | 'awaiting_approval' | 'superseded' | 'held' | 'rolling_out' | 'watching' | 'released';
  /**
   * One line, such as "Rolling out: step 2 of 4, 10% of traffic".
   */
  summary: string;
  traffic_percent: number;
  /**
   * Zero-based current canary step.
   */
  canary_step?: number;
  canary_total_steps?: number;
  created_at: string;
  deployed_by?: string;
  commit_sha?: string;
  pr_number?: number;
  tag?: string;
  reason?: string;
  /**
   * The release this one replaces; an abort returns traffic to it.
   */
  stable_deployment_id?: string;
  stable_revision?: number;
  /**
   * The newer release of the same scope that took over.
   */
  replaced_by_deployment_id?: string;
  replaced_by_revision?: number;
  /**
   * When the post-release watch stops judging the release.
   */
  watch_until?: string;
  approval?: ReleaseApprovalStatus;
  schedule?: ScheduledRelease;
  /**
   * The results of the newest decision that judged release gates.
   */
  gates?: Array<ReleaseGateResult>;
  events: Array<ReleaseTimelineEvent>;
  /**
   * Older audit history was left out.
   */
  truncated?: boolean;
};

