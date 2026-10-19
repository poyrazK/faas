/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One expected identity with safe catalog metadata and closed comparison evidence; these IDs are independent of historical episode rule keys.
 */
export type ObsRuleCoverageItem = {
  id: string;
  file_name: string;
  group_name: string;
  rule_name: string;
  state: 'matching' | 'missing' | 'changed' | 'ambiguous' | 'unknown';
  reason?: 'rule_missing' | 'definition_changed' | 'ambiguous_identity' | 'source_identity_unavailable' | 'definition_unavailable';
  expected_fingerprint: string;
  observed_fingerprint?: string;
  observed_candidates: number;
  /**
   * Changed fields in expression then for then keep_firing_for then labels order; empty unless comparison is known changed.
   */
  changed_fields: Array<'expression' | 'for' | 'keep_firing_for' | 'labels'>;
};

