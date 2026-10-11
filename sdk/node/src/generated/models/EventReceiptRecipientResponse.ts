/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventReceiptCancellationResponse } from './EventReceiptCancellationResponse.js';
import type { EventReceiptExecutionResponse } from './EventReceiptExecutionResponse.js';
import type { EventReceiptRecoveryAction } from './EventReceiptRecoveryAction.js';
import type { EventReceiptRecoveryResponse } from './EventReceiptRecoveryResponse.js';
import type { EventReceiptRoutingResponse } from './EventReceiptRoutingResponse.js';
/**
 * One captured or backfilled recipient with independent routing, original execution or cancellation evidence, and retained generic replay recovery.
 */
export type EventReceiptRecipientResponse = {
  /**
   * Captured at acceptance or added by a historical backfill.
   */
  origin?: 'acceptance' | 'backfill';
  /**
   * Originating backfill job while its metadata is retained.
   */
  backfill_job_id?: string;
  /**
   * Account-authenticated backfill job inspection while the job and current target are available.
   */
  backfill_job_url?: string;
  /**
   * Immutable captured subscription or notification identifier.
   */
  subscription_id: string;
  /**
   * Published workflow captured as an event recipient.
   */
  workflow_name?: string;
  /**
   * Durable run id after event-trigger admission.
   */
  workflow_run_id?: string;
  /**
   * Current workflow run status when retained.
   */
  workflow_run_status?: string;
  app_id: string;
  /**
   * Current slug when the target still belongs to the authenticated account.
   */
  app_slug?: string;
  routing: EventReceiptRoutingResponse;
  execution?: EventReceiptExecutionResponse;
  recovery?: EventReceiptRecoveryResponse;
  cancellation?: EventReceiptCancellationResponse;
  /**
   * No original invocation was found; record_unavailable after enqueue may reflect independent retention and is not a success claim.
   */
  execution_unavailable?: 'not_enqueued' | 'record_unavailable' | 'cancel_pending';
  /**
   * Applicable selective recovery requests; empty when no action is currently eligible. Authorization and state are checked again on POST.
   */
  recovery_actions: Array<EventReceiptRecoveryAction>;
  /**
   * Account-authenticated routing and replay history for this recipient.
   */
  fanout_history_url?: string;
  /**
   * Account-authenticated handler attempt history including trusted replays.
   */
  attempt_history_url?: string;
};

