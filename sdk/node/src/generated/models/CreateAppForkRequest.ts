/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Body of `POST /v1/apps/{slug}/forks`. Every field is optional.
 */
export type CreateAppForkRequest = {
  /**
   * How long the fork lives. Default 3600 (1 h), maximum 14400 (4 h).
   */
  ttl_seconds?: number;
  /**
   * Capture the app's newest running instance now and fork that
   * capture, instead of the deployment's last snapshot. The capture
   * pauses the instance briefly. Refused (409 `live_fork_refused`)
   * when no instance is running, a capture is in flight, or one was
   * taken in the last minute.
   *
   */
  live?: boolean;
};

