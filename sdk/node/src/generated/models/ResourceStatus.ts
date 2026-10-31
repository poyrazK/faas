/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Condition } from './Condition.js';
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
};

