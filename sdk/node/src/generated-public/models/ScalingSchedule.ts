/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One recurring window that raises the warm floor (ADR-195). `cron` is a five-field expression evaluated in the policy's `timezone`; each fire opens a window of `duration_s` seconds during which the app's floor is at least `min_instances`. Cron expresses instants rather than intervals, so the duration is what makes a window — and it needs no special case for a window that crosses midnight.
 */
export type ScalingSchedule = {
  /**
   * Five-field cron expression (minute hour day-of-month month day-of-week), evaluated in the policy timezone.
   */
  cron: string;
  /**
   * How long the window stays open after each fire, in seconds. Between 60 and 604800 (7 days). The floor of 60 s exists because the scheduler sweep is coarser than that, so a shorter window could close before any tick observed it — billing a floor that never produced an instance.
   */
  duration_s: number;
  /**
   * Warm floor while the window is open. Must be > 0: a schedule raises the floor and cannot lower it.
   */
  min_instances: number;
};

