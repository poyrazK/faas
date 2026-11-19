/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { InsightStepRequest } from './InsightStepRequest.js';
/**
 * A cohort to save. did: customers that sent the event at least min_count times in the last within_days days; did_not: customers active in the last 30 days that did not send it; funnel: customers that completed the steps within the window, starting in the last within_days days.
 */
export type InsightCohortRequest = {
  /**
   * Cohort key, used in gregale.cohort rule conditions.
   */
  key: string;
  kind: 'did' | 'did_not' | 'funnel';
  /**
   * The event for did and did_not.
   */
  event?: string;
  /**
   * Property values the did or did_not event must carry.
   */
  where?: Record<string, string>;
  /**
   * Look-back in days, at most the plan's analytics retention.
   */
  within_days: number;
  /**
   * did: minimum events (default 1).
   */
  min_count?: number;
  /**
   * The funnel's steps.
   */
  steps?: Array<InsightStepRequest>;
  /**
   * funnel: Go duration from the first step to the last, 1h to 720h.
   */
  window?: string;
};

