/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIServerKey } from './DataAPIServerKey.js';
import type { DataAPIUserAuth } from './DataAPIUserAuth.js';
/**
 * Access settings the runtime reports for live reads, sent after start and hourly. It replaces the stored settings unless the stored ones come from a runtime process that started later; turning usage reporting off forgets them. Holds key hashes, never keys.
 */
export type DataAPIAccessConfig = {
  version: 1;
  schemas: Array<string>;
  auth?: DataAPIUserAuth;
  public_read?: Array<string>;
  server_keys?: Array<DataAPIServerKey>;
  /**
   * Relations signed-in users may read without row-level security; live reads apply the same exemptions.
   */
  allow_without_rls?: Array<string>;
  /**
   * When the reporting runtime process started. A report never replaces settings from a process that started later.
   */
  started_at?: string;
};

