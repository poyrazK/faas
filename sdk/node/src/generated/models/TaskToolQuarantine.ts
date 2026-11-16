/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { TaskToolQuarantineResult } from './TaskToolQuarantineResult.js';
import type { TaskToolRecoveryQualification } from './TaskToolRecoveryQualification.js';
/**
 * Separate fixed owner approval and one admitted candidate claim-fence attempt. Applying is uncertain until read-only Task-database evidence is observed. It cannot renew or reuse restore authority.
 */
export type TaskToolQuarantine = {
  qualification: TaskToolRecoveryQualification;
  state: 'qualified' | 'applying' | 'quarantined';
  decision?: 'approved' | 'revoked';
  authorizer_key_id?: string;
  decided_at?: string;
  attempt_sha256?: string;
  claimed_at?: string;
  result?: TaskToolQuarantineResult;
};

