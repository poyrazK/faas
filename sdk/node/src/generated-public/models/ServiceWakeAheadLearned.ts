/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ServiceWakeAheadTarget } from './ServiceWakeAheadTarget.js';
/**
 * What the gateways measured for this caller app (ADR-1152): its top service edges and what wake-ahead did over the evidence window. Present on GET; source is unavailable when gateway metrics cannot be read.
 */
export type ServiceWakeAheadLearned = {
  source: 'live' | 'unavailable';
  /**
   * Window of the outcome counts, for example 24h.
   */
  window: string;
  /**
   * Cold wakes of this app the evidence covers (older wakes decay).
   */
  caller_wakes: number;
  targets: Array<ServiceWakeAheadTarget>;
};

