/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RoutePriorityQueueClass } from './RoutePriorityQueueClass.js';
/**
 * How the app's warm-capacity queue behaved per priority class over the evidence window. Present on GET; source is unavailable when gateway metrics cannot be read.
 */
export type RoutePriorityQueueStats = {
  source: 'live' | 'unavailable';
  /**
   * Window of the counts, for example 24h.
   */
  window: string;
  classes: Array<RoutePriorityQueueClass>;
};

