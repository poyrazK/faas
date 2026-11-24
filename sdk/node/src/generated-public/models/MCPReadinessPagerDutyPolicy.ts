/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Masked native PagerDuty settings without plaintext, ciphertext or receiver fingerprint.
 */
export type MCPReadinessPagerDutyPolicy = {
  /**
   * Monotonic PagerDuty receiver policy version; zero denotes webhook primary without native configuration.
   */
  version: number;
  enabled: boolean;
  /**
   * Effective Events API region; US for unconfigured or disabled policies.
   */
  region: 'us' | 'eu';
  /**
   * Fixed mask returned only for an enabled PagerDuty integration.
   */
  integration_key_masked?: '***';
};

