/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A single customer data upstream. The plaintext host is replaced by
 * `host_redacted_hash` (sha256(salt||host) 8-hex prefix); the §11
 * barrier means the wire format never carries the customer's DSN.
 *
 */
export type DataUpstreamResponse = {
  id: string;
  /**
   * Whether the row was captured by the classifier (FAAS_DATA_PLACEMENT=1) or added via PUT (explicit).
   */
  source: 'inferred' | 'explicit';
  kind: 'postgres' | 'redis' | 'mongo' | 'cassandra' | 'clickhouse' | 'elasticsearch' | 'opensearch' | 'rabbitmq' | 'kafka' | 'nats' | 'minio' | 'memcached' | 'etcd' | 's3' | 'https_api';
  /**
   * SHA-256 hex of (HostHashSalt||host). 64 lowercase hex chars, matching the schema CHECK constraint.
   */
  host_redacted_hash: string;
  /**
   * Compatibility field name. First 8 hex chars of host_redacted_hash; safe for operator correlation (8 chars = ~4B capacity).
   */
  host_last4?: string;
  port: number;
  /**
   * ADR-090 deployment-scope filter (3..40 chars, lowercase alnum + dash). Echoes the value persisted on the row; absent when the default scope applies.
   */
  scope?: string;
  /**
   * ADR-098 amendment (issue #954) widens the dedupe key to include `deployment_scope` so staging-vs-prod upstreams don't collide on the same app. Echoes the value persisted on the row; absent when the default scope applies.
   */
  deployment_scope?: string;
  /**
   * Region hint (nullable). Empty on capture; populated by the operator or the classify-flow follow-up.
   */
  declared_region?: string;
  /**
   * Most recent probe RTT (ms). Omitted when no probe yet.
   */
  last_rtt_ms?: number;
  /**
   * Timestamp of the most recent probe. Omitted when no probe yet.
   */
  last_probed_at?: string;
  created_at: string;
  last_seen_at: string;
};
