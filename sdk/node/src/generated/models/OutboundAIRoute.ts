/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteCanary } from './OutboundAIRouteCanary.js';
import type { OutboundAIRouteTarget } from './OutboundAIRouteTarget.js';
/**
 * An ordered list of managed AI integrations. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path}. An openai-format route accepts OpenAI chat completions and embeddings and translates them for each target's provider.
 */
export type OutboundAIRoute = {
  id: string;
  name: string;
  format: 'native' | 'openai';
  targets: Array<OutboundAIRouteTarget>;
  created_at: string;
  updated_at: string;
  /**
   * How a call picks its first target.
   */
  strategy: 'ordered' | 'weighted' | 'latency' | 'cost';
  /**
   * A cost route's latency target, or null.
   */
  max_ttfb_ms?: number | null;
  /**
   * The route's model canary, or null.
   */
  canary?: (OutboundAIRouteCanary | null);
};

