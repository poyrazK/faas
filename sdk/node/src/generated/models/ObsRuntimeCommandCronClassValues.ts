/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Schedules in this ownership scope and included unknown-age subset. Known due timing is required only when schedules exceeds unknown_age; otherwise both timing fields are absent. Disabled, suspended, account-held, not-due and unknown classes have entirely unknown ages. Age starts at the first unconsumed nominal boundary using canonical timezone and DST rules; it is not continuous eligibility, command execution delay or a new stall threshold. Integer ceiling is wire safety, not an admission quota.
 */
export type ObsRuntimeCommandCronClassValues = {
  schedules: number;
  unknown_age: number;
  oldest_due_at?: string;
  oldest_known_due_age_seconds?: number;
};

