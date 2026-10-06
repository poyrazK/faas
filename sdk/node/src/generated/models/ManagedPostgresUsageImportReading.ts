/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One normalized integer meter quantity from retained provider evidence.
 */
export type ManagedPostgresUsageImportReading = {
  meter: 'active_seconds' | 'compute_unit_seconds' | 'storage_byte_seconds' | 'history_byte_seconds' | 'egress_bytes' | 'operations';
  quantity: number;
};

