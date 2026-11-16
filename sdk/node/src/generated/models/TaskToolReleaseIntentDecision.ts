/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Owner verdict bound to an immutable Task intent digest and its retained source fingerprint.
 */
export type TaskToolReleaseIntentDecision = {
  intent_sha256: string;
  release_fingerprint: string;
  decision: 'approve' | 'reject' | 'revoke';
};

