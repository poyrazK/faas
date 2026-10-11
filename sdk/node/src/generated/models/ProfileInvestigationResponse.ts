/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileInvestigation } from './ProfileInvestigation.js';
import type { ProfileInvestigationWindowStatus } from './ProfileInvestigationWindowStatus.js';
/**
 * Saved metadata and authenticated dashboard link, with explicit per-window eligibility.
 */
export type ProfileInvestigationResponse = {
  saved: ProfileInvestigation;
  /**
   * Relative dashboard link that requires an authenticated account with access to this app.
   */
  url: string;
  baseline_status: ProfileInvestigationWindowStatus;
  candidate_status: ProfileInvestigationWindowStatus;
};

