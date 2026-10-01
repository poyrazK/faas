/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explainable boolean or named-variant decision against a configuration version.
 */
export type FlagDecision = {
  flag: string;
  value: (boolean | string);
  /**
   * Present as variant for named-variant decisions; omitted for legacy boolean decisions.
   */
  type?: 'boolean' | 'variant';
  config_version: number;
  rule_id?: string;
  reason: 'flag_missing' | 'default' | 'disabled' | 'customer_missing' | 'subject_missing' | 'rule_match' | 'configuration_stale' | 'type_mismatch';
  /**
   * Boolean rollout bucket or weighted variant assignment bucket.
   */
  bucket?: number;
  /**
   * Eligibility bucket for rollout-gated variant rules.
   */
  rollout_bucket?: number;
  source: 'configuration' | 'fallback' | 'inherited';
  inherited_from?: {
    app_id: string;
    environment_id: string;
  };
};

