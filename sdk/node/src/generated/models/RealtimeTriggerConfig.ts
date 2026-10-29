/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Config of a `kind: realtime` trigger. Each message of the bound consumer group is delivered as one
 * invocation. The group must already exist in the caller's account with `lease_seconds` of at least 120, and binds
 * to at most one trigger; while bound, direct claims and settlements return 409 `realtime_group_bound_to_trigger`.
 * The group's ordering, filters, retry policy and dead-letter queue govern delivery. The binding is immutable;
 * only `max_concurrency` can change.
 *
 */
export type RealtimeTriggerConfig = {
  endpoint_id: string;
  channel: string;
  group: string;
  /**
   * Parallel invocations; 0 means 1. Also capped by the plan's concurrency.
   */
  max_concurrency?: number;
};

