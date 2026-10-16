/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupMessage } from './RealtimeConsumerGroupMessage.js';
/**
 * Realtime Dead Letter Export Record.
 */
export type RealtimeDeadLetterExportRecord = {
  type: 'dead_letter';
  group_id: string;
  sequence: string;
  dead_since: string;
  attempts: number;
  failure_code: string;
  fencing_token: string | null;
  delivery_deadline: string | null;
  deadline_invalid: boolean;
  ordering_key: string;
  priority: number;
  /**
   * Nominal dead-letter expiry; retained rows can survive until cleanup runs
   */
  expires_at: string | null;
  message: RealtimeConsumerGroupMessage;
};

