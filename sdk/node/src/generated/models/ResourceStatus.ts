/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Condition } from './Condition.js';
import type { ManagedPostgresBackupStatus } from './ManagedPostgresBackupStatus.js';
import type { ManagedPostgresMaintenanceStatus } from './ManagedPostgresMaintenanceStatus.js';
import type { ManagedPostgresReplicaStatus } from './ManagedPostgresReplicaStatus.js';
import type { ManagedPostgresStorageStatus } from './ManagedPostgresStorageStatus.js';
/**
 * A resource's status subresource: conditions derived when read, never stored.
 */
export type ResourceStatus = {
  kind: 'app' | 'deployment' | 'database' | 'domain' | 'cron' | 'job_run';
  /**
   * The app slug, deployment ID, database name, domain, cron ID or job run ID.
   */
  name: string;
  /**
   * When the evidence behind the conditions was read.
   */
  observed_at: string;
  conditions: Array<Condition>;
  storage?: ManagedPostgresStorageStatus;
  maintenance?: ManagedPostgresMaintenanceStatus;
  backup?: ManagedPostgresBackupStatus;
  replica?: ManagedPostgresReplicaStatus;
};

