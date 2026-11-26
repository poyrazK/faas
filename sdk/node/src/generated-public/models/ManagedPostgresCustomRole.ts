/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * A custom group role declared on a managed database (ADR-20261116213000).
 */
export type ManagedPostgresCustomRole = {
  database_id: string;
  name: string;
  bypass_rls: boolean;
  /**
   * Access modes whose logins may SET ROLE to it (migration, read_write, read_only); read_write includes session logins.
   */
  granted_to: Array<'migration' | 'read_write' | 'read_only'>;
  /**
   * Other custom roles of the database it is a member of.
   */
  member_of: Array<string>;
  /**
   * Access modes whose logins start every session as this role.
   */
  default_for: Array<'read_write' | 'read_only'>;
  /**
   * pending until applied to the database, ready after, deleting until a deleted role is dropped.
   */
  state: 'pending' | 'ready' | 'deleting';
  last_error_code?: string;
  created_at: string;
  updated_at: string;
};

