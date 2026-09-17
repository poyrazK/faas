/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Gregale-owned private-network definition.
 */
export type PrivateNetwork = {
  id: string;
  name: string;
  region: string;
  /**
   * Canonical IPv4 RFC1918 range, /16 through /28.
   */
  cidr: string;
  status: 'ready' | 'error';
  status_detail?: string;
  created_at?: string;
  updated_at?: string;
};

