/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIEvaluationItem } from './OutboundAIEvaluationItem.js';
import type { OutboundAIEvaluationTargetStats } from './OutboundAIEvaluationTargetStats.js';
import type { PutOutboundAIRouteCanaryRequest } from './PutOutboundAIRouteCanaryRequest.js';
/**
 * An evaluation of a route target. verdict is set when state is done, error_code when it is failed. items are returned only when one evaluation is read.
 */
export type OutboundAIEvaluation = {
  id: string;
  route_id: string;
  target: number;
  judge_integration_id: string;
  judge_model: string;
  sample_size: number;
  max_loss_rate: number;
  canary: (PutOutboundAIRouteCanaryRequest | null);
  state: 'pending' | 'running' | 'done' | 'failed';
  verdict: ('pass' | 'fail' | null);
  error_code: string | null;
  samples: number;
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
  items?: Array<OutboundAIEvaluationItem>;
};

