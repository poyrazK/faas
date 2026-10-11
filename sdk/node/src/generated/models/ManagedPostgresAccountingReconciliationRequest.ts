/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Operator-attested immutable backend identity and actual provider shutdown for one unresolved legacy tombstone. The operator verifies artifact ownership and lineage; the server does not fetch or authenticate the source. Missing lookup results are not shutdown evidence.
 */
export type ManagedPostgresAccountingReconciliationRequest = {
  /**
   * Durable reconciliation identity.
   */
  reconciliation_id: string;
  /**
   * Deleted accountable database with no provider identity.
   */
  database_id: string;
  /**
   * Exact immutable catalog backend ID.
   */
  backend_id: string;
  /**
   * Exact immutable catalog backend fingerprint.
   */
  backend_fingerprint: string;
  /**
   * Verified opaque provider identity; include branch identity where required by the adapter.
   */
  provider_resource_id: string;
  /**
   * Actual confirmed provider shutdown at or after catalog creation; microsecond precision.
   */
  shutdown_at: string;
  /**
   * Actual evidence observation at or after shutdown and retained ledger observations; no future timestamps.
   */
  observed_at: string;
  /**
   * Retained nonsecret artifact reference; no credentials or signed URLs.
   */
  evidence_reference: string;
  /**
   * SHA-256 of the operator-verified source artifact.
   */
  evidence_sha256: string;
  /**
   * Audited reason for reconciliation.
   */
  reason: string;
  /**
   * Omit for preview; required for apply using the returned revision.
   */
  expected_revision?: string;
};

