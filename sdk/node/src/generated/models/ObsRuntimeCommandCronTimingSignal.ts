/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
import type { ObsRuntimeCommandCronTimingName } from './ObsRuntimeCommandCronTimingName.js';
/**
 * One fixed timing counter comparison with original sample clocks. Cumulative buckets and count form a coherent histogram group; unknown timestamps are independent. Only comparable intervals retain an exact nonnegative delta, including zero.
 */
export type ObsRuntimeCommandCronTimingSignal = {
  signal: ObsRuntimeCommandCronTimingName;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

