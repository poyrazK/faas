/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Checks of the text a model sends back, before the caller sees it. The detectors redact or block as on requests; blocked_terms block refuses a response that contains one of the policy's blocked terms; moderation runs the policy's moderation check on the response, without prompt_injection, and flags or blocks it. A refused whole response answers 422 outbound_ai_response_blocked or outbound_ai_response_flagged; a refused stream ends with an error event. Tool call arguments and reasoning text are not checked.
 */
export type OutboundAIResponsePolicy = {
  email?: '' | 'off' | 'redact' | 'block';
  payment_card?: '' | 'off' | 'redact' | 'block';
  iban?: '' | 'off' | 'redact' | 'block';
  secret?: '' | 'off' | 'redact' | 'block';
  blocked_terms?: '' | 'off' | 'block';
  /**
   * Uses the policy's moderation check settings, which must be complete even while moderation.action is off.
   */
  moderation?: '' | 'off' | 'flag' | 'block';
};

