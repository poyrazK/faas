/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PlanDetectedBy } from './PlanDetectedBy.js';
/**
 * One discovered unit of work. Mirrors reposcan.Workload.
 */
export type PlanWorkload = {
  name: string;
  root_dir: string;
  dockerfile?: string;
  command: Array<string>;
  class?: 'http' | 'graphql' | 'grpc' | 'job' | 'worker' | 'server' | 'unknown';
  /**
   * cron expression when declared (CronJob, render, serverless)
   */
  schedule?: string;
  ports: Array<number>;
  /**
   * KEYS only — never values; spec §11 forbids logging secrets
   */
  env_keys?: Array<string>;
  /**
   * detector provenance, e.g. compose.yaml: api
   */
  source?: string;
  tier?: 'single' | 'convention' | 'workspace' | 'compose' | 'unknown';
  detected_by?: PlanDetectedBy;
};

