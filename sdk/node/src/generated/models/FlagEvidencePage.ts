/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagRequestEvidence } from './FlagRequestEvidence.js';
/**
 * Stable window of up to 100 retained request aggregates; preserve filters for pagination.
 */
export type FlagEvidencePage = {
  items: Array<FlagRequestEvidence>;
  next_cursor?: string;
  window_start: string;
  window_end: string;
};

