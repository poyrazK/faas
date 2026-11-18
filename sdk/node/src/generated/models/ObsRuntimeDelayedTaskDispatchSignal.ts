/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskOperationPoint } from './ObsRuntimeAppTaskOperationPoint.js';
import type { ObsRuntimeAppTaskOperationReason } from './ObsRuntimeAppTaskOperationReason.js';
import type { ObsRuntimeDelayedTaskDispatchName } from './ObsRuntimeDelayedTaskDispatchName.js';
/**
 * One process-local delayed-task dispatch counter. First usable points are baselines. Comparable points retain exact nonnegative differences including zero. Resets, changed processes, stale or missing samples and uncertain windows omit delta; missing values are never zero. Other outcomes remain independently usable.
 */
export type ObsRuntimeDelayedTaskDispatchSignal = {
  outcome: ObsRuntimeDelayedTaskDispatchName;
  state: 'baseline' | 'increased' | 'unchanged' | 'missing' | 'stale' | 'conflicting' | 'unavailable';
  reason?: ObsRuntimeAppTaskOperationReason;
  current?: ObsRuntimeAppTaskOperationPoint;
  previous?: ObsRuntimeAppTaskOperationPoint;
  delta?: number;
};

