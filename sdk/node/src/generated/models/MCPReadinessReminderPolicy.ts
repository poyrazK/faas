/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-incident lifetime creation budget. Both zero disables; otherwise interval is 60–86400 seconds and budget is 1–10. Reconfiguration never resets the count.
 */
export type MCPReadinessReminderPolicy = {
  interval_seconds: number;
  max_reminders: number;
};

