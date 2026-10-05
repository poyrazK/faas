/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Correlation handle for an accepted explicit app wake.
 */
export type AppWakeResponse = {
  /**
   * Wake id on the admitted instance and wake timeline (the running instance's when already_running).
   */
  wake_id: string;
  /**
   * True when the app already had a routable running instance and no wake was queued.
   */
  already_running?: boolean;
  /**
   * The running instance, present when already_running is true.
   */
  instance_id?: string;
};

