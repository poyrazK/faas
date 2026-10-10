/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckExclusion } from './AutomationCheckExclusion.js';
import type { AutomationCheckScenario } from './AutomationCheckScenario.js';
/**
 * Simulation check metadata. server_verified is set only for server-issued publishing receipts; legacy or plain client evidence is unverified. Bound to the saved draft hash and version. Contains metadata only; no sample inputs, outputs, or failure text. Checked time must be within the past day (five minutes of future clock skew allowed).
 */
export type AutomationCheckEvidence = {
  /**
   * True only when publication used a stored server-issued receipt. Clients cannot attest to this flag.
   */
  readonly server_verified?: boolean;
  definition_hash: string;
  checked_version: number;
  checked_at: string;
  scenarios: Array<AutomationCheckScenario>;
  coverage_required: boolean;
  /**
   * True when coverage_remaining is zero; required coverage must pass before publishing.
   */
  coverage_passed: boolean;
  coverage_remaining: number;
  exclusions: Array<AutomationCheckExclusion>;
};

