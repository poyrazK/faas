/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One unified queue invocation, broker trigger, outbound webhook, job run, workflow run, or event routing failure dead-letter event.
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
  /**
   * event_route is an event recipient whose routing failed and whose receipt was pruned; payload is the published envelope, error_kind the failure_code, and it cannot be replayed.
   */
  source: 'invocation' | 'trigger_record' | 'webhook_delivery' | 'job_run' | 'workflow_run' | 'event_route';
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

