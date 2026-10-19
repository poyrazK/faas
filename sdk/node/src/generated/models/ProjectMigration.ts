/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectMigrationService } from './ProjectMigrationService.js';
/**
 * Creator-owned migration receipt linking selected services to exact deployment identities.
 */
export type ProjectMigration = {
  /**
   * Monotonic journal version required to resume the reviewed set of services.
   */
  version: number;
  /**
   * Durable project migration identity.
   */
  id: string;
  /**
   * Creator-owned project slug.
   */
  project: string;
  /**
   * Project identity after reconciliation.
   */
  project_id?: string;
  /**
   * Checksum of the original reviewed source archive.
   */
  source_sha256: string;
  status: 'prepared' | 'applying' | 'tracking' | 'attention';
  /**
   * Project migration recovery guidance.
   */
  reason?: string;
  services: Array<ProjectMigrationService>;
  created_at: string;
  updated_at: string;
  retry_after?: string;
};

