/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPromptMessage } from './OutboundAIPromptMessage.js';
import type { OutboundAIPromptParams } from './OutboundAIPromptParams.js';
import type { OutboundAIPromptTarget } from './OutboundAIPromptTarget.js';
/**
 * A new version. messages, defaults and params together are at most 64 KiB.
 */
export type PushOutboundAIPromptVersionRequest = {
  /**
   * Replaces the prompt's description when set.
   */
  description?: string;
  messages: Array<OutboundAIPromptMessage>;
  /**
   * Values for variables a call may omit.
   */
  defaults?: Record<string, string>;
  target: OutboundAIPromptTarget;
  params?: OutboundAIPromptParams;
  note?: string;
  /**
   * Make the version live. A prompt's first version always is.
   */
  live?: boolean;
};

