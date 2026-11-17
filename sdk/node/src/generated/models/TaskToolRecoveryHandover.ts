/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolQuarantine } from './TaskToolQuarantine.js';
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
import type { TaskToolRecoveryResult } from './TaskToolRecoveryResult.js';
import type { TaskToolRetirement } from './TaskToolRetirement.js';
/**
 * Permanent executor fence and separate owner recovery authority. Settlement requires an exit acknowledgement and the native mutation barrier. Restore and quarantine have separate authority and retained results.
 */
export type TaskToolRecoveryHandover = {
  retirement?: TaskToolRetirement;
  quarantine?: TaskToolQuarantine;
  result?: TaskToolRecoveryResult;
  id: string;
  operation_id: string;
  revision: number;
  state: 'stopping' | 'settled';
  requested_at: string;
  settled_at?: string;
  qualification?: TaskToolRecoveryQualification;
  decision?: 'approved' | 'revoked';
  authorizer_key_id?: string;
  decided_at?: string;
};

