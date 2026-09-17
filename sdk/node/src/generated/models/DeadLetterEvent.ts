/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One unified queue invocation or broker trigger dead-letter event.
 */
export type DeadLetterEvent = {
  id: string;
  source: 'invocation' | 'trigger_record';
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

