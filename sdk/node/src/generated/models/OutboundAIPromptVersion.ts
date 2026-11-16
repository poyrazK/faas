/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPromptMessage } from './OutboundAIPromptMessage.js';
import type { OutboundAIPromptParams } from './OutboundAIPromptParams.js';
import type { OutboundAIPromptTarget } from './OutboundAIPromptTarget.js';
/**
 * One immutable version of a managed AI prompt.
 */
export type OutboundAIPromptVersion = {
  version: number;
  messages: Array<OutboundAIPromptMessage>;
  /**
   * The distinct placeholder names
   */
  variables: Array<string>;
  defaults: Record<string, string>;
  target: OutboundAIPromptTarget;
  params: OutboundAIPromptParams;
  note: string;
  created_at: string;
};

