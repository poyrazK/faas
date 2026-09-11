/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Stable API consumer identity. No credential secret is returned.
 */
export type APIConsumerResponse = {
  id: string;
  app_id: string;
  external_ref: string;
  name: string;
  status: 'active' | 'revoked';
  created_at: string;
  updated_at: string;
  revoked_at?: string | null;
};

