/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Optional suppression field of private platform-alert incident evidence.
 * Current suppression requires exact verification of the immutable episode
 * against fresh Prometheus rules plus an unambiguous exact-label match in
 * Alertmanager. Changed or external labels remain unmatched. StartsAt must
 * fall within the current activation at millisecond wire precision and now;
 * updatedAt must be fresh and
 * endsAt in the future. Resolved episodes are historical and not applicable.
 * Silenced and inhibited booleans are null unless current state is verified.
 * Suppression is independent of operator triage snooze and delivery receipt.
 *
 */
export type ObsAlertSuppression = {
  state: 'active' | 'suppressed' | 'unprocessed' | 'unmatched' | 'stale' | 'unavailable' | 'disabled' | 'not_applicable' | 'unknown';
  reason?: string;
  checked_at: string;
  updated_at?: string;
  silenced: boolean | null;
  inhibited: boolean | null;
};

