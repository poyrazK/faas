/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresQueryError } from './ManagedPostgresQueryError.js';
/**
 * Pooled error collection settings and retained events matching the filter.
 */
export type ManagedPostgresErrorHistory = {
  database_id: string;
  enabled: boolean;
  since: string;
  events: Array<ManagedPostgresQueryError>;
  /**
   * More retained events match the filter than the requested limit.
   */
  has_more: boolean;
};

