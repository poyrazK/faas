/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explainable boolean decision against a configuration version.
 */
export type FlagDecision = {
  flag: string;
  value: boolean;
  config_version: number;
  rule_id?: string;
  reason: 'flag_missing' | 'default' | 'disabled' | 'customer_missing' | 'rule_match' | 'configuration_stale';
  bucket?: number;
  source: 'configuration' | 'fallback';
};

