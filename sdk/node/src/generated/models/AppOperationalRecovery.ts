/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOperationalRollback } from './AppOperationalRollback.js';
import type { AppWorkerRecovery } from './AppWorkerRecovery.js';
import type { RuntimeConfigRestartStatusResponse } from './RuntimeConfigRestartStatusResponse.js';
/**
 * Bounded app-scoped pending rollbacks and pending or failed restart handoffs. Availability and truncation are explicit; accepted work does not establish completed recovery.
 */
export type AppOperationalRecovery = {
  workers?: AppWorkerRecovery;
  /**
   * Explicit scoped summaries include only attributable operations; app-wide restarts are excluded.
   */
  coverage?: string;
  rollbacks_available: boolean;
  restarts_available: boolean;
  rollbacks_truncated: boolean;
  restarts_truncated: boolean;
  rollbacks: Array<AppOperationalRollback>;
  restarts: Array<RuntimeConfigRestartStatusResponse>;
};

