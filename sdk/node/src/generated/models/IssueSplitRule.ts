/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { IssueSplitMatcher } from './IssueSplitMatcher.js';
/**
 * Immutable exact-match routing rule. Creating it moves eligible retained matches still assigned to the original source into an open branch. Earlier enabled rules take priority. Expired history remains on the source. Toggling affects future captures only; retries retain their existing routing identity.
 */
export type IssueSplitRule = {
  id: string;
  source_issue_id: string;
  target_issue_id: string;
  priority: number;
  matcher: IssueSplitMatcher;
  enabled: boolean;
  initial_moved_event_count: number;
  unavailable_history_event_count: number;
  created_at: string;
  updated_at: string;
};

