/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Data delivered with outbound.ai_quality.drift to every app bound to the integration when a model's quality signal moves away from its previous 7 days. alert_id is stable across retries.
 */
export type OutboundAIQualityDriftWebhookPayload = {
  alert_id: string;
  integration_id: string;
  integration_name: string;
  usage_date: string;
  model: string;
  metric: 'truncated' | 'refused' | 'empty' | 'invalid_json' | 'tool_error' | 'bad_feedback' | 'evaluation_score';
  /**
   * Basis points of answered (or rated) calls; hundredths of a point for evaluation_score.
   */
  current_value: number;
  baseline_value: number;
  /**
   * Calls the current value was taken over.
   */
  current_count: number;
  baseline_count: number;
  detected_at: string;
};

