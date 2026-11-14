/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A higher price for a long prompt. When a call's input tokens (cached reads and cache writes included) exceed above_input_tokens, its input-side prices are multiplied by input_percent/100 and its output prices by output_percent/100.
 */
export type OutboundAILongContextTier = {
  above_input_tokens: number;
  input_percent: number;
  output_percent: number;
};

