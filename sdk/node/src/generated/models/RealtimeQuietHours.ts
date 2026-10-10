/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Daily local-time interval during which push delivery is deferred.
 */
export type RealtimeQuietHours = {
  /**
   * Named timezone, for example Europe/Rome; Local is rejected.
   */
  timezone: string;
  start: string;
  end: string;
};

