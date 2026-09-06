/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataUpstreamHistoryBucket } from './DataUpstreamHistoryBucket.js';
/**
 * Historical probe series for one redacted upstream and region.
 */
export type DataUpstreamHistoryResponse = {
  /**
   * SHA-256 hex of (HostHashSalt||host); plaintext hosts never appear on this surface.
   */
  host_redacted_hash: string;
  kind: 'postgres' | 'redis' | 'mongo' | 'cassandra' | 'clickhouse' | 'elasticsearch' | 'opensearch' | 'rabbitmq' | 'kafka' | 'nats' | 'minio' | 'memcached' | 'etcd' | 's3' | 'https_api';
  port: number;
  /**
   * Env scope associated with the upstream.
   */
  scope?: string;
  /**
   * Deployment scope from the ADR-098 issue #954 overlay.
   */
  deployment_scope?: string;
  region: string;
  buckets: Array<DataUpstreamHistoryBucket>;
};

