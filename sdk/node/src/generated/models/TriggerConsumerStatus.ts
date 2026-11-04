/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The scheduler's last observation of a trigger's broker consumer.
 * Absent until the trigger's first poll.
 *
 */
export type TriggerConsumerStatus = {
  /**
   * `degraded`: the last poll failed (see last_error). `stale`: no
   * poll for 30 s (the trigger is disabled, its app cannot run, or
   * no scheduler polls it).
   *
   */
  liveness: 'healthy' | 'degraded' | 'stale' | 'not_observed' | 'unknown';
  last_poll_at?: string;
  last_success_at?: string;
  last_error_at?: string;
  last_error?: string;
  /**
   * The broker's backlog for the consumer. Kafka: high watermark
   * less the group's committed offset, summed over partitions.
   * NATS JetStream: the consumer's pending messages. Redis Streams:
   * the group's lag.
   *
   */
  lag_messages?: number;
  /**
   * Age of the oldest record the last poll returned.
   */
  lag_age_seconds?: number;
};

