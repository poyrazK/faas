/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One case's result in a managed AI prompt evaluation.
 */
export type OutboundAIPromptEvaluationItem = {
  case: number;
  verdict: 'win' | 'tie' | 'loss' | 'candidate_error' | 'judge_error';
  baseline_status: number | null;
  candidate_status: number | null;
  baseline_cost_millicents: number;
  candidate_cost_millicents: number;
  baseline_ttfb_ms: number | null;
  candidate_ttfb_ms: number | null;
  baseline_output_text?: string;
  candidate_output_text?: string;
};

