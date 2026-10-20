/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIServerKey } from './DataAPIServerKey.js';
import type { DataAPIUserAuth } from './DataAPIUserAuth.js';
/**
 * Access settings the runtime reports for live reads without a user token, sent after start and hourly. It replaces the stored settings; turning usage reporting off forgets them. Holds key hashes, never keys.
 */
export type DataAPIAccessConfig = {
  version: 1;
  schemas: Array<string>;
  auth?: DataAPIUserAuth;
  public_read?: Array<string>;
  server_keys?: Array<DataAPIServerKey>;
};

