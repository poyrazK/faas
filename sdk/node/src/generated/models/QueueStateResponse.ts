/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * 200 — queue depth / in-flight / oldest-pending stats. Read-only.
 */
export type QueueStateResponse = {
  app_slug: string;
  plan: 'free' | 'hobby' | 'pro' | 'scale';
  /**
   * MaxQueueDepth for the plan.
   */
  plan_cap: number;
  /**
   * Pending + dispatching row count.
   */
  depth: number;
  /**
   * Rows with a live dispatch lease (state=dispatching, lease_expires_at > now or NULL).
   */
  in_flight: number;
  /**
   * Oldest pending row's created_at; null when the queue is empty.
   */
  oldest_pending_at?: string | null;
  /**
   * Convenience field — seconds since oldest_pending_at; null when the queue is empty.
   */
  oldest_pending_age_seconds?: number | null;
  generated_at: string;
  /**
   * Every pending or in-delivery invocation of the app per source (async_invoke, inbound_webhook, queue, ...), so async work waiting for the scheduler is visible beside the queue's depth. Omitted when nothing waits.
   */
  backlog_by_source?: Record<string, number>;
};

