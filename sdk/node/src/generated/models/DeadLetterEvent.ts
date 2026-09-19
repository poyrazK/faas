/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One unified queue invocation, broker trigger, outbound webhook, job run, or workflow run dead-letter event.
 */
export type DeadLetterEvent = {
  id: string;
  /**
   * Owning app when the event is app-scoped.
   */
  app_id?: string;
  /**
   * Owning app slug when the event is app-scoped.
   */
  app_slug?: string;
  source: 'invocation' | 'trigger_record' | 'webhook_delivery' | 'job_run' | 'workflow_run';
  source_id: string;
  /**
   * Invocation source or trigger kind.
   */
  origin?: string;
  trigger_id?: string;
  payload: Record<string, any>;
  headers: Record<string, any>;
  error_kind: string;
  error_detail: Record<string, any>;
  retry_count: number;
  first_failed_at: string;
  last_failed_at: string;
  replayed_at?: string | null;
  created_at: string;
};

