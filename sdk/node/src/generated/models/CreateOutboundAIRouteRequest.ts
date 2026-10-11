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
   * native (default) needs one ai_provider across targets; openai accepts openai, azure_openai, anthropic and gemini targets, and every target but openai must name its model (ADR-991).
   */
  format?: 'native' | 'openai';
  targets: Array<OutboundAIRouteTarget>;
};

