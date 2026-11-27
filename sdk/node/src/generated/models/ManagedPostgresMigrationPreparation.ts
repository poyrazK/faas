/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresMigrationPreparationBlocker } from './ManagedPostgresMigrationPreparationBlocker.js';
/**
 * Schema preparation on the fenced target before the final source write pause.
 */
export type ManagedPostgresMigrationPreparation = {
  state: 'preparing' | 'prepared' | 'blocked' | 'paused';
  reason?: string;
  indexes_total: number;
  indexes_ready: number;
  sequences: number;
  blockers?: Array<ManagedPostgresMigrationPreparationBlocker>;
};

