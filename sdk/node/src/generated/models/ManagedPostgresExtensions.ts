/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ManagedPostgresExtensionVersion } from './ManagedPostgresExtensionVersion.js';
/**
 * Installed PostgreSQL extensions and versions available to the database.
 */
export type ManagedPostgresExtensions = {
  installed: Array<ManagedPostgresExtensionVersion>;
  available: Array<ManagedPostgresExtensionVersion>;
  incompatible_extensions?: Array<ManagedPostgresExtensionVersion>;
};

