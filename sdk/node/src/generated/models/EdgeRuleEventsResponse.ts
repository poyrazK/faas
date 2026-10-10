/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleEventResponse } from './EdgeRuleEventResponse.js';
/**
 * A page of sampled edge-rule matches (ADR-964).
 */
export type EdgeRuleEventsResponse = {
  /**
   * Effective window start after the plan clamp.
   */
  since: string;
  events: Array<EdgeRuleEventResponse>;
  next_cursor?: string;
};

