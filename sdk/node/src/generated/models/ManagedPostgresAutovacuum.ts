/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresAutovacuumSettings } from './ManagedPostgresAutovacuumSettings.js';
export type ManagedPostgresAutovacuum = {
  database_id: string;
  profile: 'default' | 'high-churn';
  state: 'applied' | 'pending' | 'parked' | 'unavailable';
  effective?: ManagedPostgresAutovacuumSettings;
  replica_effective?: ManagedPostgresAutovacuumSettings;
};

