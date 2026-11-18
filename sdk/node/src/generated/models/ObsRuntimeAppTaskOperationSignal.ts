/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationName } from './ObsRuntimeAppTaskOperationName.js';
import type { ObsRuntimeAppTaskOperationOutcome } from './ObsRuntimeAppTaskOperationOutcome.js';
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
/**
 * One ordered operation/outcome comparison. Baseline has only a current point. Increased and unchanged require both points, unchanged process proof and an explicit exact delta, including zero. Unknown comparisons omit delta and may retain usable points. Counter decreases are resets, never negative activity. Partial positives survive sibling gaps.
 */
export type ObsRuntimeAppTaskOperationSignal = {
  operation: ObsRuntimeAppTaskOperationName;
  outcome: ObsRuntimeAppTaskOperationOutcome;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

