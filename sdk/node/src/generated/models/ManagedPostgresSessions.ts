/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresSessionSample } from './ManagedPostgresSessionSample.js';
/**
 * Current session sample availability and tracked pooled queries.
 */
export type ManagedPostgresSessions = {
  database_id: string;
  state: 'available' | 'parked' | 'unavailable';
  sample?: ManagedPostgresSessionSample;
};

