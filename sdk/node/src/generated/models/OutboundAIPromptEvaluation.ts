/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIEvaluationTargetStats } from './OutboundAIEvaluationTargetStats.js';
import type { OutboundAIPromptEvaluationItem } from './OutboundAIPromptEvaluationItem.js';
import type { PutOutboundAIPromptCanaryRequest } from './PutOutboundAIPromptCanaryRequest.js';
/**
 * A comparison of a candidate prompt version against the live version.
 */
export type OutboundAIPromptEvaluation = {
  id: string;
  prompt: string;
  baseline_version: number;
  candidate_version: number;
  judge_integration_id: string;
  judge_model: string;
  max_loss_rate: number;
  canary: (PutOutboundAIPromptCanaryRequest | null);
  state: 'pending' | 'running' | 'done' | 'failed';
  verdict: 'pass' | 'fail';
  error_code: string | null;
  cases: number;
  wins: number;
  ties: number;
  losses: number;
  candidate_errors: number;
  judge_errors: number;
  baseline: OutboundAIEvaluationTargetStats;
  candidate: OutboundAIEvaluationTargetStats;
  canary_started: boolean;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  items?: Array<OutboundAIPromptEvaluationItem>;
  /**
   * The items' answer texts were left out; reading them needs admin or secrets:read.
   */
  content_withheld?: boolean;
};

