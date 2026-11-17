/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskKindName } from './ObsRuntimeAppTaskKindName.js';
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
import type { ObsRuntimeAppTaskResultName } from './ObsRuntimeAppTaskResultName.js';
import type { ObsRuntimeAppTaskResultSource } from './ObsRuntimeAppTaskResultSource.js';
/**
 * One ordered completion or recovery counter comparison. Kind is required for completion and absent for recovery. First valid points are baselines; compared points require fresh evidence at both pinned times, matching original uptime clocks and unchanged sampled process continuity. Comparable signals retain an exact nonnegative delta including zero. Unknown comparisons omit delta and may retain usable points. Missing evidence is never synthesized as zero; sibling positives survive gaps.
 */
export type ObsRuntimeAppTaskResultSignal = {
  source: ObsRuntimeAppTaskResultSource;
  kind?: ObsRuntimeAppTaskKindName;
  outcome: ObsRuntimeAppTaskResultName;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

