/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresRestoreDrillStatus } from './ManagedPostgresRestoreDrillStatus.js';
/**
 * Progress and outcome of a temporary point-in-time restore exercise.
 */
export type ManagedPostgresRestoreDrill = (ManagedPostgresRestoreDrillStatus & {
  database_id: string;
  request_id: string;
  drill_id: string;
});

