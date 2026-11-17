/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Personal-owner source review decision bound to both immutable hosted source digests.
 */
export type HostedTaskSourceDecision = {
  /**
   * Complete hosted source intent digest explicitly reviewed by the personal owner.
   */
  intent_sha256: string;
  /**
   * Exact retained hosted source-review fingerprint included in this owner decision.
   */
  review_sha256: string;
  /**
   * Owner verdict on this hosted source review, independent of future execution authorization.
   */
  decision: 'approve' | 'reject' | 'revoke';
};

