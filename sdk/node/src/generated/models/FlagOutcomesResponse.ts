/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagOutcome } from './FlagOutcome.js';
/**
 * Operational outcomes grouped by evaluated flag type and value over one bounded window.
 */
export type FlagOutcomesResponse = {
  outcomes: Array<FlagOutcome>;
  /**
   * True when additional low-volume cohorts were omitted after retaining the 100 highest-volume groups.
   */
  truncated: boolean;
  window_start: string;
  window_end: string;
};

