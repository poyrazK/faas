/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Estimated change had this route's requests never reached the app, with every other request unchanged.
 */
export type RouteAwakeWhatIf = {
  awake_seconds_saved: number;
  gb_hours_saved: number;
  /**
   * Change in cold wakes; positive when other routes would find the app parked more often.
   */
  wakes_change: number;
};

