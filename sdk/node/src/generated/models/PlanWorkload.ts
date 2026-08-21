/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
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
  /**
   * ADR-124 blast-radius projection. create = workload is new to the account; update = existing app matches (root_dir, name).
   */
  action?: 'create' | 'update';
  /**
   * ADR-124: app row ID the update targets. Empty iff action == create.
   */
  existing_app_id?: string;
};

