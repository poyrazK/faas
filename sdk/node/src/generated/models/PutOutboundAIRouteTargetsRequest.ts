/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteTarget } from './OutboundAIRouteTarget.js';
/**
 * The complete ordered target list that replaces an AI route's targets, with its strategy (default ordered). New targets end the route's canary and reset its targets' recent attempts.
 */
export type PutOutboundAIRouteTargetsRequest = {
  targets: Array<OutboundAIRouteTarget>;
  /**
   * The route's strategy from now on, as on creation; omitted means ordered.
   */
  strategy?: 'ordered' | 'weighted' | 'latency' | 'cost';
  /**
   * The cost route's latency target from now on, as on creation; omitted means none.
   */
  max_ttfb_ms?: number;
};

