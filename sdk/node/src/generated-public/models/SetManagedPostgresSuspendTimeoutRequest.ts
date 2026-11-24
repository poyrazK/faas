/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * How long a scale-to-zero database stays up after its last activity before it suspends.
 */
export type SetManagedPostgresSuspendTimeoutRequest = {
  /**
   * Seconds of inactivity before the database suspends; null returns it to the platform default.
   */
  seconds: number | null;
};

