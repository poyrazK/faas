/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIServerKeyPermissionSet } from './DataAPIServerKeyPermissionSet.js';
/**
 * One server-key rotation slot as the runtime reports it, the SHA-256 of the key and any permissions.
 */
export type DataAPIServerKey = {
  slot: 'a' | 'b';
  sha256: string;
  permissions?: DataAPIServerKeyPermissionSet;
};

