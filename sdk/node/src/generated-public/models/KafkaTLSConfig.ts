/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Kafka TLS material. MinVersion is forced to TLS 1.2 at
 * decoder time regardless of what the wire sends
 * (pkg/sched/poller_kafka.go::buildKafkaTLSConfig). When
 * ClientCert + ClientKey are both set the decoder performs
 * a half-wired guard — if only one is set, decode returns
 * an error rather than the poller falling through silently
 * to PLAINTEXT over an apparent mTLS endpoint.
 *
 */
export type KafkaTLSConfig = {
  /**
   * PEM-encoded CA bundle. Optional; if omitted the system trust store is used.
   */
  ca_cert?: string;
  /**
   * PEM-encoded client cert for mTLS.
   */
  client_cert?: string;
  /**
   * PEM-encoded client key for mTLS, accepted only on writes. Omit inside a supplied TLS block to preserve the stored key.
   */
  client_key?: string;
  /**
   * Response-only marker indicating that a client key is configured; no credential material is returned.
   */
  readonly client_key_set?: boolean;
  /**
   * Skip TLS verification. Hobby plan rejects this
   * (TLSSkipVerifyAllowed=false in pkg/api/limits.go);
   * Pro and Scale accept it for self-signed brokers.
   *
   */
  skip_verify?: boolean;
};

