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
   * How a call picks its first target (ADR-1390). ordered (the default) follows route order; weighted draws by target weight; latency picks the healthy target with the lowest recent mean time to first byte; cost picks the healthy target with the lowest recent mean cost per call. The other targets follow as fallbacks.
   */
  strategy?: 'ordered' | 'weighted' | 'latency' | 'cost';
  /**
   * A cost route's latency target; targets slower on average go after those within it.
   */
  max_ttfb_ms?: number;
};

