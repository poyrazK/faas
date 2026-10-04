/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Safe staging and SQL verification evidence for a source binding.
 */
export type ManagedPostgresCutoverMember = {
  source_binding_id: string;
  environment_key: string;
  access: 'read_write' | 'read_only' | 'migration';
  state: 'pending' | 'sealed' | 'revoked';
  verified_at?: string;
};

