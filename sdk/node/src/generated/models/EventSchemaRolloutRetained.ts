/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSchemaRolloutValidation } from './EventSchemaRolloutValidation.js';
/**
 * Retained sample coverage and compatibility observations for the candidate event schema.
 */
export type EventSchemaRolloutRetained = {
  requested: boolean;
  from?: string;
  until?: string;
  cutoff_at?: string;
  /**
   * Account receipts scanned, including unrelated sources and types.
   */
  scanned_count: number;
  examined_count: number;
  valid_count: number;
  invalid_count: number;
  /**
   * Oversized, malformed, or invalid retained envelopes excluded from validation.
   */
  unreadable_count: number;
  /**
   * Account scan, matching payload limit, or byte budget prevented checking all candidates.
   */
  truncated: boolean;
  /**
   * Retention and bounded sampling never certify complete history.
   */
  history_complete: boolean;
  results: Array<EventSchemaRolloutValidation>;
};

