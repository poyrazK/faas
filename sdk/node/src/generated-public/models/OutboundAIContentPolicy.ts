/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIModeration } from './OutboundAIModeration.js';
import type { OutboundAIResponsePolicy } from './OutboundAIResponsePolicy.js';
/**
 * An integration's content guardrails. Each detector is off, redacts its findings before the request reaches the provider, or blocks the request. A request containing a blocked term, compared without case, is blocked. The gateway checks the string values of JSON bodies and the text fields of forms.
 */
export type OutboundAIContentPolicy = {
  email: '' | 'off' | 'redact' | 'block';
  payment_card: '' | 'off' | 'redact' | 'block';
  iban: '' | 'off' | 'redact' | 'block';
  /**
   * API keys, tokens and private keys.
   */
  secret: '' | 'off' | 'redact' | 'block';
  blocked_terms: Array<string>;
  moderation?: OutboundAIModeration;
  response?: OutboundAIResponsePolicy;
};

