/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteTarget } from './OutboundAIRouteTarget.js';
/**
 * An ordered list of managed AI integrations. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path} (ADR-990). An openai-format route accepts OpenAI chat completions and translates them for Anthropic and Gemini targets (ADR-991).
 */
export type OutboundAIRoute = {
  id: string;
  name: string;
  format: 'native' | 'openai';
  targets: Array<OutboundAIRouteTarget>;
  created_at: string;
  updated_at: string;
};

