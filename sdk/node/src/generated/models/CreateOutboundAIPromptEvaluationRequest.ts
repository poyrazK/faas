/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PutOutboundAIPromptCanaryRequest } from './PutOutboundAIPromptCanaryRequest.js';
/**
 * A candidate version, the variable sets to run it on, and the judge model.
 */
export type CreateOutboundAIPromptEvaluationRequest = {
  version: number;
  /**
   * Variable sets; each is rendered with the candidate and the live version.
   */
  cases: Array<Record<string, string>>;
  judge_integration_id: string;
  judge_model: string;
  max_loss_rate?: number;
  canary?: PutOutboundAIPromptCanaryRequest;
};

