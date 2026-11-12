/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigChangeReceipt } from './ConfigChangeReceipt.js';
/**
 * Durable value-free receipt of an atomic variable batch.
 */
export type EnvBatchResponse = {
  request_id: string;
  app_id: string;
  scope: string;
  /**
   * Immutable environment identity when the original named batch was pinned.
   */
  environment_id?: string;
  updated_keys: Array<string>;
  deleted_keys: Array<string>;
  config_change: ConfigChangeReceipt;
};

