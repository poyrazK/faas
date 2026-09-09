/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Logical bucket metadata without upstream credentials or placement details.
 */
export type ObjectBucket = {
  id: string;
  name: string;
  scope: string;
  region: string;
  state: 'provisioning' | 'ready' | 'deleting';
  public: boolean;
  serve_at?: string;
  created_at: string;
};

