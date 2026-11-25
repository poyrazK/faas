/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresTableHealthSample } from './ManagedPostgresTableHealthSample.js';
/**
 * Table health sample or the reason a sample could not be collected.
 */
export type ManagedPostgresTableHealth = {
  database_id: string;
  state: 'available' | 'parked' | 'unavailable';
  sample?: ManagedPostgresTableHealthSample;
};

