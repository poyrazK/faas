/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One sample: a captured call of the first target and the candidate's replay of it. A judge error counts as a tie. Output texts are redacted and cut to 4 KiB.
 */
export type OutboundAIEvaluationItem = {
  log_id: string;
  verdict: 'win' | 'tie' | 'loss' | 'candidate_error' | 'judge_error';
  candidate_status: number | null;
  candidate_cost_millicents: number;
  candidate_ttfb_ms: number | null;
  baseline_cost_millicents: number;
  baseline_ttfb_ms: number | null;
  baseline_output_text: string;
  candidate_output_text: string;
};

