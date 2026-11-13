/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProjectEnvironmentBranchDatabase } from './ProjectEnvironmentBranchDatabase.js';
import type { ProjectEnvironmentBranchWorkload } from './ProjectEnvironmentBranchWorkload.js';
/**
 * How an environment branches from its parent.
 */
export type ProjectEnvironmentBranchResponse = {
  /**
   * Slug of the parent environment.
   */
  parent: string;
  /**
   * The moment the branch copied its parent.
   */
  branch_point: string;
  /**
   * When the branch is deleted.
   */
  expires_at: string;
  egress: string;
  /**
   * Whether the branch's instances join their workloads' private networks.
   */
  private_networks: 'off' | 'join';
  /**
   * 1 for a branch of an ordinary environment
   */
  depth: number;
  /**
   * Whether the copies of the parent's managed PostgreSQL databases exist. The branch's workloads start only when this is ready.
   */
  data: 'ready' | 'pending' | 'failed';
  /**
   * Why copying the parent's data failed or is being retried.
   */
  data_error?: string;
  workloads?: Array<ProjectEnvironmentBranchWorkload>;
  /**
   * When the branch was created. Reported by the branch's own view.
   */
  created_at?: string;
  /**
   * The managed PostgreSQL copies the branch owns. Reported by the branch's own view.
   */
  databases?: Array<ProjectEnvironmentBranchDatabase>;
};

