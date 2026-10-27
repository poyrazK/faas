/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional per-source budget spent only by selected proxied application 4xx responses. When statuses is omitted, 401 and 403 are counted. In enforce mode, subsequent requests are rejected before authentication and wake after this budget is exhausted.
 */
export type PreAuthFailedResponseLimit = {
  /**
   * Continuous token refill per minute; no greater than the parent route's requests_per_second times 60.
   */
  failures_per_minute: number;
  /**
   * Maximum consecutive failed responses; no greater than the parent route's burst.
   */
  burst: number;
  /**
   * Optional shared failed-response budget across gateway replicas. Defaults to local. Central mode checks one of 1,024 opaque source shards before compute and records selected application failures afterward. Concurrent failures can incur bounded debt. On database errors the replica-local bucket remains active.
   */
  coordination?: 'local' | 'central';
  /**
   * Selected application response statuses. Defaults to [401, 403]. Only 4xx codes except 429 are supported.
   */
  statuses?: Array<number>;
};

