/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
import type { ObsRuntimeCommandCronOutcomeName } from './ObsRuntimeCommandCronOutcomeName.js';
import type { ObsRuntimeCommandCronOutcomeSource } from './ObsRuntimeCommandCronOutcomeSource.js';
/**
 * One fixed source/outcome comparison. Baselines retain a current point without reporting historical events as new. Compared points require fresh evidence at both pinned times, unchanged sampled process proof, matching original scrape clocks and coherent uptime advance. Comparable signals retain an exact nonnegative delta including zero; unknown comparisons omit delta and may retain usable points. Missing values are never zero; sibling positives survive gaps.
 */
export type ObsRuntimeCommandCronOutcomeSignal = {
  source: ObsRuntimeCommandCronOutcomeSource;
  outcome: ObsRuntimeCommandCronOutcomeName;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

