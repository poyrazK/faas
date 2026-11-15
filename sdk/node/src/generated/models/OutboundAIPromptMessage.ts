/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A templated message; content may hold {{name}} placeholders.
 */
export type OutboundAIPromptMessage = {
  role: 'system' | 'developer' | 'user' | 'assistant';
  content: string;
};

