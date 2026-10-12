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
   * native (default) needs one ai_provider across targets; openai accepts openai, azure_openai, anthropic, gemini, bedrock and vertex targets, every target but openai must name its model, and a vertex target's integration must name one /v1/projects/{project}/locations/{location} path prefix (ADR-991, ADR-993).
   */
  format?: 'native' | 'openai';
  targets: Array<OutboundAIRouteTarget>;
};

