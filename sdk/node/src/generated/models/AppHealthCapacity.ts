/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Measured serving replica counts, with explicit evidence availability.
 */
export type AppHealthCapacity = {
  /**
   * False when instance evidence is unavailable or truncated.
   */
  known: boolean;
  required: number;
  ready: number;
  starting: number;
  unready: number;
  unknown: number;
};

