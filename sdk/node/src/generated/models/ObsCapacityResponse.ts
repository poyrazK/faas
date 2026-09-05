/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsCapacityNode } from './ObsCapacityNode.js';
import type { ObsCapacitySummary } from './ObsCapacitySummary.js';
/**
 * Fleet capacity snapshot - aggregate headroom plus per-node rows.
 */
export type ObsCapacityResponse = {
  generated_at: string;
  summary: ObsCapacitySummary;
  nodes: Array<ObsCapacityNode>;
};

