/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RealtimeConsumerGroupMessage } from './RealtimeConsumerGroupMessage.js';
/**
 * Realtime Consumer Group Dead Letter.
 */
export type RealtimeConsumerGroupDeadLetter = {
  message: RealtimeConsumerGroupMessage;
  attempts: number;
  failure_code: string;
  retention_started_at: string | null;
  expires_at: string | null;
  warning_at: string | null;
  cleanup_after: string | null;
};

