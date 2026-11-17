/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolQuarantineRequest } from './TaskToolQuarantineRequest.js';
import type { TaskToolRecoveryRequest } from './TaskToolRecoveryRequest.js';
import type { TaskToolReleaseExecutionRequest } from './TaskToolReleaseExecutionRequest.js';
import type { TaskToolRetirementRequest } from './TaskToolRetirementRequest.js';
/**
 * Owner verdict bound to an immutable Task intent digest and its retained source fingerprint.
 */
export type TaskToolReleaseIntentDecision = {
  retirement?: TaskToolRetirementRequest;
  quarantine?: TaskToolQuarantineRequest;
  intent_sha256: string;
  release_fingerprint: string;
  decision: 'approve' | 'reject' | 'revoke' | 'authorize' | 'recovery_stop' | 'recovery_qualify' | 'recovery_approve' | 'recovery_revoke' | 'recovery_quarantine_qualify' | 'recovery_quarantine_approve' | 'recovery_quarantine_revoke' | 'recovery_retire_qualify' | 'recovery_retire_approve' | 'recovery_retire_revoke';
  execution?: TaskToolReleaseExecutionRequest;
  recovery?: TaskToolRecoveryRequest;
};

