/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EventSubscriptionDeadlineRecipient } from './EventSubscriptionDeadlineRecipient.js';
/**
 * One read-only live page ordered by effective deadline, acceptance time and internal receipt identity. No aggregate health or frozen membership is implied. Empty recipients are not proof of successful delivery.
 */
export type EventSubscriptionDeadlinePage = {
  coverage: 'retained_captured_pending_application_deadlines';
  app_id: string;
  subscription_id: string;
  observed_at: string;
  window_at: string;
  read_only: boolean;
  recipients: Array<EventSubscriptionDeadlineRecipient>;
  /**
   * A lookahead row existed in this page snapshot; later pages can still be empty as routing state changes.
   */
  has_more: boolean;
  /**
   * Present only when has_more is true. Carries no authorization.
   */
  next_cursor?: string;
};

