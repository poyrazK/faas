/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer-visible capability with the account plan gate resolved.
 */
export type CapabilityStatus = {
  key: string;
  name: string;
  category: string;
  description: string;
  maturity: 'internal' | 'preview' | 'beta' | 'ga';
  plans: Array<'free' | 'hobby' | 'pro' | 'scale'>;
  docs_url: string;
  /**
   * Stable acceptance-test handle for this capability.
   */
  acceptance: string;
  /**
   * Whether the calling account may use the capability under its current plan.
   */
  enabled: boolean;
};

