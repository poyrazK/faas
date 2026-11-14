/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsServingProbe } from './ObsServingProbe.js';
import type { ObsServingProbeCounts } from './ObsServingProbeCounts.js';
import type { ObsServingProbeInventory } from './ObsServingProbeInventory.js';
/**
 * Coverage of expected serving probes from independent deployment intent and current coverage-rule evaluations. No direct probes are executed.
 */
export type ObsServingHealth = {
  generated_at: string;
  scope: string;
  state: 'healthy' | 'degraded' | 'partial' | 'not_applicable';
  reason?: string;
  max_age_seconds: number;
  inventory: ObsServingProbeInventory;
  /**
   * Null when inventory is unknown or unconfigured; an explicitly empty configured fleet has zero counts.
   */
  counts: (ObsServingProbeCounts | null);
  probes: Array<ObsServingProbe>;
  limitations: Array<string>;
};

