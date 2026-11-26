/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The declaration of a custom role; absent fields take their defaults.
 */
export type PutManagedPostgresCustomRoleRequest = {
  bypass_rls?: boolean;
  /**
   * Access modes whose logins may SET ROLE to it; session is stored as read_write.
   */
  granted_to?: Array<'migration' | 'read_write' | 'read_only' | 'session'>;
  member_of?: Array<string>;
  /**
   * When present, replaces the access modes whose logins start as this role; absent keeps the current ones that are still granted.
   */
  default_for?: Array<'read_write' | 'read_only'>;
};

