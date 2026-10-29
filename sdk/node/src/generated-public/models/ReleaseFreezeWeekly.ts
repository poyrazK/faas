/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A weekly freeze window: it opens on each listed day at `start` in `time_zone` and lasts `duration_minutes`, so it may run past midnight or into the next week.
 */
export type ReleaseFreezeWeekly = {
  /**
   * Weekdays the window opens on.
   */
  days: Array<'sun' | 'mon' | 'tue' | 'wed' | 'thu' | 'fri' | 'sat'>;
  /**
   * Local opening time, HH:MM.
   */
  start: string;
  /**
   * How long each window lasts.
   */
  duration_minutes: number;
  /**
   * IANA time zone, such as Europe/Istanbul or UTC.
   */
  time_zone: string;
};

