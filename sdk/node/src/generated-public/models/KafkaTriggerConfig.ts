/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { KafkaSASLConfig } from './KafkaSASLConfig.js';
import type { KafkaTLSConfig } from './KafkaTLSConfig.js';
/**
 * Decoded `config` for kind=kafka triggers. The wire-level
 * blob lives in Trigger.config; this is the SDK's
 * server-side shape.
 *
 */
export type KafkaTriggerConfig = {
  /**
   * Bootstrap broker list (host:port per entry).
   */
  brokers: Array<string>;
  topic: string;
  group: string;
  tls?: KafkaTLSConfig;
  sasl?: KafkaSASLConfig;
};

