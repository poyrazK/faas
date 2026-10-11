/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventRecoveryNotificationReceiver } from './EventRecoveryNotificationReceiver.js';
/**
 * Receiver selection is frozen at capture, including an empty selection. Retained deliveries alone cannot prove the complete selection. Counts describe observed receivers and may be lower bounds. Unknown receivers prevent acknowledged status; no_receivers is distinct from acknowledgement. Pending captures have no receiver selection yet.
 */
export type EventRecoveryNotification = {
  kind: 'admission' | 'execution';
  event?: 'event_recovery.completed' | 'event_recovery.cancelled' | 'event_recovery.expired' | 'event_recovery.execution_finished';
  event_id?: string;
  capture_status: 'pending' | 'captured' | 'not_applicable' | 'unknown';
  captured_at?: string;
  evidence_source: 'capture_snapshot' | 'retained_outbox' | 'retained_deliveries' | 'unavailable';
  recipients_known: boolean;
  selected_recipient_count?: number;
  counts_complete: boolean;
  acknowledgement_status: 'unacknowledged' | 'acknowledged' | 'no_receivers' | 'unknown' | 'not_applicable';
  pending_count: number;
  in_flight_count: number;
  succeeded_count: number;
  failed_count: number;
  dead_count: number;
  awaiting_relay_count: number;
  unknown_count: number;
  receivers: Array<EventRecoveryNotificationReceiver>;
};

