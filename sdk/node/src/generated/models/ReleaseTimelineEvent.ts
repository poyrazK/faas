/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RolloutDecision } from './RolloutDecision.js';
/**
 * One thing that happened to a release.
 */
export type ReleaseTimelineEvent = {
  at: string;
  kind: 'created' | 'scheduled' | 'schedule_started' | 'schedule_blocked' | 'schedule_cancelled' | 'approval_requested' | 'approved' | 'rejected' | 'approval_expired' | 'rollout_started' | 'advanced' | 'traffic_changed' | 'held' | 'completed' | 'aborted' | 'rollback_requested' | 'rolled_back' | 'watch_passed' | 'superseded' | 'note';
  /**
   * What happened, in one sentence.
   */
  summary: string;
  /**
   * A person's email, or Gregale for automatic steps.
   */
  actor?: string;
  /**
   * The release's traffic share after this event.
   */
  traffic_percent?: number;
  /**
   * Further holds with the same rule this event stands for.
   */
  repeats?: number;
  decision?: RolloutDecision;
};

