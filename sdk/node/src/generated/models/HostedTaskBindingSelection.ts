/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
export type HostedTaskBindingSelection = {
  intent_sha256: string;
  review_sha256: string;
  namespace: string;
  /**
   * Owner-only native managed PostgreSQL reference; no credential value.
   */
  web_database_ref: string;
  /**
   * Owner-only native managed PostgreSQL reference; no credential value.
   */
  worker_database_ref: string;
  /**
   * Owner-only native managed PostgreSQL reference; no credential value.
   */
  migration_database_ref: string;
  /**
   * Owner-only native managed PostgreSQL reference; no credential value.
   */
  observer_database_ref: string;
};

