/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One drift the check found: a model's metric today against its previous 7 days (ADR 20261021171712). Rates are basis points of answered calls (of rated calls for bad_feedback); evaluation_score is the mean score in hundredths.
 */
export type OutboundAIQualityAlert = {
  id: string;
  integration_id: string;
  usage_date: string;
  model: string;
  metric: 'truncated' | 'refused' | 'empty' | 'invalid_json' | 'tool_error' | 'bad_feedback' | 'evaluation_score';
  current_value: number;
  baseline_value: number;
  current_count: number;
  baseline_count: number;
  detected_at: string;
};

