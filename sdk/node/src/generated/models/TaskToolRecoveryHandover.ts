/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
/**
 * Permanent executor fence and separate owner recovery authority. Settlement requires an exit acknowledgement and the native mutation barrier. No recovery apply API consumes this approval yet.
 */
export type TaskToolRecoveryHandover = {
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

