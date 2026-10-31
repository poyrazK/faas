/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConfigRevision } from './ConfigRevision.js';
/**
 * A config revision with its variables and secret versions. Secret values are never returned.
 */
export type ConfigRevisionDetail = (ConfigRevision & {
  variables: Record<string, string>;
  secret_versions: Record<string, number>;
});

