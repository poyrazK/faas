/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AutomationCheckEvidence } from './AutomationCheckEvidence.js';
import type { WorkflowSpec } from './WorkflowSpec.js';
/**
 * Immutable published automation definition with actor and rollout provenance.
 */
export type AutomationRevisionResponse = {
  check_evidence?: AutomationCheckEvidence;
  /**
   * Immutable published revision identifier.
   */
  version: number;
  definition: WorkflowSpec;
  /**
   * SHA-256 of the canonical JSON encoding of definition.
   */
  definition_hash: string;
  /**
   * When this immutable history record was stored; for legacy snapshots this is the migration time.
   */
  recorded_at: string;
  /**
   * True for the one current publication copied into history during rollout; older history was not retained.
   */
  legacy_snapshot: boolean;
  /**
   * Account that published this revision, or owned the legacy snapshot at rollout.
   */
  published_by_account_id: string;
  /**
   * API key used for the publish when the request used key authentication; omitted for session authentication and legacy snapshots.
   */
  published_by_api_key_id?: string;
};

