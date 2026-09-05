/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Provider-independent access binding between one Gregale API key and one logical bucket.
 */
export type ObjectBucketAccessGrant = {
  key_id: string;
  key_label: string;
  key_status: 'active' | 'grace' | 'revoked';
  permission: 'read' | 'write' | 'read_write';
  created_at: string;
  updated_at: string;
};

