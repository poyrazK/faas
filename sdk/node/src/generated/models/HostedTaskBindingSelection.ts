/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Personal-owner selection of native Task binding references for one approved retained source intent, never plaintext credentials.
 */
export type HostedTaskBindingSelection = {
  intent_sha256: string;
  review_sha256: string;
  namespace: string;
  /**
   * Existing read_write native managed PostgreSQL secret reference on the reviewed web app; never a credential value.
   */
  web_database_ref: string;
  /**
   * Existing read_write native managed PostgreSQL secret reference on the reviewed worker app sharing the selected queue.
   */
  worker_database_ref: string;
  /**
   * Worker app reference for its separate migration-only PostgreSQL role; qualification never runs a migration.
   */
  migration_database_ref: string;
  /**
   * Native read_only PostgreSQL secret reference on the reviewed observer app, isolated from runtime and migration roles.
   */
  observer_database_ref: string;
};

