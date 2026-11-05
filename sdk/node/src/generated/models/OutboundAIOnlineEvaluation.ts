/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Score sample_percent of answered app calls from 1 to 5 with judge_model through judge_integration_id, one of the account's AI integrations, metered under the caller evaluation.
 */
export type OutboundAIOnlineEvaluation = {
  sample_percent: number;
  judge_integration_id: string;
  judge_model: string;
  /**
   * What a good answer does for this app
   */
  rubric?: string;
};

