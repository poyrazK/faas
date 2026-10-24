/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Customer-visible capability with account entitlement and existing runtime availability gates resolved. Internal capabilities are omitted.
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
   * Whether the calling account is entitled and the capability passes its existing runtime availability gate. This is not a fleet health guarantee.
   */
  enabled: boolean;
  /**
   * Stable reason when enabled is false. Plan restrictions take precedence when both gates deny access. Omitted when enabled; older servers may omit it.
   */
  unavailable_reason?: 'plan_not_entitled' | 'runtime_unavailable';
  /**
   * Customer-safe explanation and next action when enabled is false. Display text only; use unavailable_reason for automation. Omitted when enabled; older servers may omit it.
   */
  unavailable_detail?: string;
};

