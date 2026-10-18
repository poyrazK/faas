/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPReadinessNotificationQueue } from './MCPReadinessNotificationQueue.js';
/**
 * Bounded notification queue page from a consistent database snapshot.
 */
export type MCPReadinessNotificationQueues = {
  observed_at: string;
  queues: Array<MCPReadinessNotificationQueue>;
  next_after?: string;
};

