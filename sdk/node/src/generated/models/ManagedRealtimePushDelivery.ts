/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type ManagedRealtimePushDelivery = {
  /**
   * Earliest scheduled delivery; epoch or zero time means no schedule.
   */
  not_before?: string;
  collapse_key?: string;
  priority: 'low' | 'normal' | 'urgent';
  group_key?: string;
  group_label?: string;
  digest_id?: string;
  /**
   * Distinct message count at the most recent prepared digest attempt.
   */
  digest_count?: number;
  category: string;
  expires_at: string;
  id: string;
  device: string;
  provider: 'fcm' | 'apns' | 'webpush';
  message_id: string;
  sequence: number;
  status: 'pending' | 'sending' | 'sent' | 'failed' | 'cancelled';
  attempts: number;
  /**
   * Provider HTTP status; zero means no response.
   */
  status_code: number;
  /**
   * Sanitized outcome code; provider bodies are omitted.
   */
  code?: string;
  created_at: string;
  updated_at: string;
  next_attempt: string;
};

