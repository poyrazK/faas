/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckEvidence } from './AutomationCheckEvidence.js';
/**
 * Saved draft revision to publish, with explicit YAML takeover when needed.
 */
export type PublishAutomationRequest = {
  /**
   * Server-issued receipt required by scenarios or coverage policy; stale, expired, cross-identity and reused receipts are rejected.
   */
  check_receipt?: string;
  check_evidence?: AutomationCheckEvidence;
  /**
   * Current saved draft revision to validate and publish.
   */
  expected_version: number;
  take_over_manifest?: boolean;
};

