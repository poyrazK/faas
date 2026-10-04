/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Upsert payload for a customer data upstream. The (kind, host, port,
 * scope, deployment_scope) tuple is the deduplication key — repeating
 * the PUT updates the existing row's `last_seen_at` and (if
 * `FAAS_DATA_PLACEMENT=1`) the inferred-source tag. Plaintext host is
 * never persisted; the on-disk column is `host_redacted_hash`.
 *
 */
export type PutDataUpstreamRequest = {
  /**
   * Closed vocabulary (ADR-098 §D1). Adding a new kind requires an ADR.
   */
  kind: 'postgres' | 'redis' | 'mongo' | 'cassandra' | 'clickhouse' | 'elasticsearch' | 'opensearch' | 'rabbitmq' | 'kafka' | 'nats' | 'minio' | 'memcached' | 'etcd' | 's3' | 'https_api';
  /**
   * RFC 952/1123 hostname (no IPv4). Hashed server-side; the hashed form is what's persisted.
   */
  host: string;
  port: number;
  /**
   * ADR-090 deployment-scope filter (1..40 chars, lowercase alnum + dash). Omitted = default scope.
   */
  scope?: string;
  /**
   * ADR-098 amendment (issue #954) widens the dedupe key to include `deployment_scope` so staging-vs-prod upstreams don't collide on the same app. Same shape as `scope` (1..40 chars, lowercase alnum + dash). Omitted = default scope, the migration's SQL DEFAULT stamp.
   */
  deployment_scope?: string;
};

