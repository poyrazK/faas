/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueSplitMatcher } from './IssueSplitMatcher.js';
/**
 * Read-only counts for one original fingerprint, across all retained evidence. Unavailable lifetime history cannot be classified; no split or routing change is performed.
 */
export type IssueSplitPreview = {
  /**
   * Current retained matches a new rule would move; zero when application_status is not new-rule.
   */
  readonly eligible_event_count?: number;
  /**
   * Retained selector matches claimed by already enabled rules.
   */
  readonly shadowed_event_count?: number;
  /**
   * Newest eligible occurrence IDs after rule priority exclusions.
   */
  readonly eligible_event_ids?: Array<string>;
  /**
   * Whether this matcher can create a new rule in the current snapshot; plan capacity is checked during application.
   */
  readonly application_status?: 'new-rule' | 'existing-rule' | 'derived-branch' | 'rule-limit';
  /**
   * Existing exact matcher returned by repeat creation without replay or re-enable.
   */
  readonly existing_rule_id?: string;
  /**
   * Stable branch of the existing exact matcher.
   */
  readonly existing_target_issue_id?: string;
  original_issue_id: string;
  canonical_issue_id: string;
  matcher: IssueSplitMatcher;
  lifetime_event_count: number;
  retained_event_count: number;
  matching_event_count: number;
  nonmatching_event_count: number;
  unavailable_history_event_count: number;
  /**
   * Newest matching occurrence IDs
   */
  matching_event_ids: Array<string>;
};

