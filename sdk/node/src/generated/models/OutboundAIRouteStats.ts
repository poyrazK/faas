/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteTargetStats } from './OutboundAIRouteTargetStats.js';
/**
 * Each target's settled attempts since window_start, the window latency and cost routes judge.
 */
export type OutboundAIRouteStats = {
  strategy: 'ordered' | 'weighted' | 'latency' | 'cost';
  window_start: string;
  targets: Array<OutboundAIRouteTargetStats>;
};

