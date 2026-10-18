/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteTarget } from './OutboundAIRouteTarget.js';
/**
 * A new AI route with a DNS-label name, an optional format and 1 to 5 ordered targets.
 */
export type CreateOutboundAIRouteRequest = {
  name: string;
  /**
   * native (default) needs one ai_provider across targets; openai accepts openai, azure_openai, anthropic, gemini, bedrock and vertex targets, every target but openai must name its model, and a vertex target's integration must name one /v1/projects/{project}/locations/{location} path prefix (ADR-991, ADR-995).
   */
  format?: 'native' | 'openai';
  targets: Array<OutboundAIRouteTarget>;
  /**
   * How a call picks its first target (ADR-1434). ordered (the default) follows route order; weighted draws by target weight; latency picks the healthy target with the lowest recent mean time to first byte; cost picks the healthy target with the lowest recent mean cost per call. The other targets follow as fallbacks.
   */
  strategy?: 'ordered' | 'weighted' | 'latency' | 'cost';
  /**
   * A cost route's latency target; targets slower on average go after those within it.
   */
  max_ttfb_ms?: number;
};

