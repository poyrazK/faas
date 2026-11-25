/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresSessionSample } from './ManagedPostgresSessionSample.js';
export type ManagedPostgresSessions = {
  database_id: string;
  state: 'available' | 'parked' | 'unavailable';
  sample?: ManagedPostgresSessionSample;
};

