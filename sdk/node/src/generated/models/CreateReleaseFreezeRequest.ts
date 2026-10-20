/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseFreezeWeekly } from './ReleaseFreezeWeekly.js';
/**
 * Creates a freeze. A window takes `starts_at` (default now) and optional `ends_at`; a weekly freeze takes `weekly`; an after-rollback policy takes `after_rollback_minutes`.
 */
export type CreateReleaseFreezeRequest = {
  /**
   * App slug; omit to freeze every app of the account.
   */
  app?: string;
  kind: 'window' | 'weekly' | 'after_rollback';
  /**
   * Window start; default now.
   */
  starts_at?: string;
  /**
   * Window end; omit to freeze until lifted.
   */
  ends_at?: string;
  weekly?: ReleaseFreezeWeekly;
  /**
   * Freeze length after each automatic rollback.
   */
  after_rollback_minutes?: number;
  reason: string;
};

