/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Turns production protection on or off. on creates the baseline; off deletes the baseline objects you have not changed.
 */
export type SetAppProtectionRequest = {
  /**
   * on enrolls the app; off removes the unchanged protection objects.
   */
  state: 'on' | 'off';
};

