/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Closed producer outcome. Each source has its own ordered subset. Expected scheduling policy, rejected admission, cancellation, stale returns, unavailable deployments, lookup/store errors and sweep or admission errors remain distinct. The admission outcome unknown is an actual returned classification, separate from missing telemetry. A rejected return is not automatically an infrastructure fault.
 */
export type ObsRuntimeCommandCronOutcomeName = 'success' | 'error' | 'canceled' | 'disabled' | 'not_due' | 'account_held' | 'plan_limit' | 'replacement_wait' | 'not_owned' | 'invalid_schedule' | 'lookup_error' | 'store_unavailable' | 'no_live_deployment' | 'legacy_queued' | 'legacy_skipped' | 'legacy_stale' | 'legacy_error' | 'queued' | 'exclusive_pending' | 'coalesced' | 'skipped_overlap' | 'missed_deadline' | 'stale' | 'deployment_unavailable' | 'rejected' | 'unknown';
