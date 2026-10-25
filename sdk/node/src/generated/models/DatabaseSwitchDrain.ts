/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A fenced app's instances by outcome: retired with a native receipt, parked (no process; snapshot invalidated), still pending, or blocking the switch.
 */
export type DatabaseSwitchDrain = {
  retired: number;
  parked: number;
  pending: number;
  blocked: number;
};

