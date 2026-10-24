/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseFreezeWeekly } from './ReleaseFreezeWeekly.js';
/**
 * A deploy freeze on production releases of one app or, without `app`, of every app of the account (ADR-20261019071733).
 */
export type ReleaseFreeze = {
  id: string;
  /**
   * The covered app's slug; omitted for an account-wide freeze.
   */
  app?: string;
  kind: 'window' | 'weekly' | 'after_rollback';
  /**
   * Window start.
   */
  starts_at?: string;
  /**
   * Window end; omitted for a window that lasts until lifted.
   */
  ends_at?: string;
  weekly?: ReleaseFreezeWeekly;
  /**
   * How long an after-rollback policy freezes an app Gregale rolled back automatically.
   */
  after_rollback_minutes?: number;
  reason: string;
  /**
   * automatic_rollback for the window an after-rollback policy created.
   */
  source: 'manual' | 'automatic_rollback';
  /**
   * The rolled-back deployment behind an automatic freeze.
   */
  source_deployment_id?: string;
  created_by: string;
  created_at: string;
  /**
   * Whether the freeze withholds production releases now.
   */
  active: boolean;
  /**
   * When an inactive window or weekly freeze next starts, within a week.
   */
  next_starts_at?: string;
};

