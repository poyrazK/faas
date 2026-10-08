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
};

