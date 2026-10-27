/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileGateOverride } from './ProfileGateOverride.js';
import type { RolloutDecision } from './RolloutDecision.js';
/**
 * Body for POST /v1/deployments/{id}/canary/advance. expected_step is the persisted canary step observed by the progression worker; APID derives the next traffic percentage and rejects stale observations.
 */
export type AdvanceCanaryRequest = {
  /**
   * Reserved for the lease-bound internal worker. Public requests reject true. Rollback intent cannot fall back to promotion when gate context changes.
   */
  profile_gate_rollback?: boolean;
  profile_gate_override?: ProfileGateOverride;
  /**
   * The canary_step the caller read before requesting the next stage.
   */
  expected_step: number;
  /**
   * Reserved for Gregale's Safe Deploy worker. Public requests that set it are rejected with 422.
   */
  decision?: RolloutDecision;
};

