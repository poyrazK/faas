/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AppOperationalRollback } from './AppOperationalRollback.js';
import type { RuntimeConfigRestartStatusResponse } from './RuntimeConfigRestartStatusResponse.js';
/**
 * Bounded app-scoped pending rollbacks and pending or failed restart handoffs. Availability and truncation are explicit; accepted work does not establish completed recovery.
 */
export type AppOperationalRecovery = {
  rollbacks_available: boolean;
  restarts_available: boolean;
  rollbacks_truncated: boolean;
  restarts_truncated: boolean;
  rollbacks: Array<AppOperationalRollback>;
  restarts: Array<RuntimeConfigRestartStatusResponse>;
};

