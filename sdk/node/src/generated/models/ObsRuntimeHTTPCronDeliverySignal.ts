/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
import type { ObsRuntimeHTTPCronDeliveryName } from './ObsRuntimeHTTPCronDeliveryName.js';
/**
 * One process-local HTTP delivery outcome counter. First usable points are baselines. Comparable points retain exact nonnegative differences including zero. Resets, process changes, stale or missing samples and uncertain windows omit delta; missing values are never zero. Other outcomes can remain usable independently.
 */
export type ObsRuntimeHTTPCronDeliverySignal = {
  outcome: ObsRuntimeHTTPCronDeliveryName;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

