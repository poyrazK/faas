/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Switches attack mode on for a duration, or moves the end of an active one.
 */
export type EnableEdgeAttackModeRequest = {
  /**
   * How long attack mode stays on from now. Switching it on while it is on restarts the clock with this duration.
   */
  duration_seconds?: number;
};

