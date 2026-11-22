/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObjectNotificationRule } from './ObjectNotificationRule.js';
/**
 * Durable normalized bucket notification configuration.
 */
export type ObjectBucketNotifications = {
  bucket_id: string;
  revision: number;
  /**
   * Rules owned by declarative compute reconciliation. Full manual replacement clears ownership.
   */
  managed_rule_ids?: Array<string>;
  rules: Array<ObjectNotificationRule>;
};

