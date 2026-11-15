/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPrompt } from './OutboundAIPrompt.js';
import type { OutboundAIPromptVersion } from './OutboundAIPromptVersion.js';
/**
 * The prompt and the version a push stored, or the identical latest version.
 */
export type PushOutboundAIPromptVersionResponse = {
  prompt: OutboundAIPrompt;
  version: OutboundAIPromptVersion;
  /**
   * False when the push matched the latest version.
   */
  created: boolean;
};

