/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseDiffPackage } from './ReleaseDiffPackage.js';
/**
 * How the builds' SBOM packages differ. Each list holds at most 100 entries.
 */
export type ReleaseDiffDependencies = {
  /**
   * False when either release has no build SBOM.
   */
  known: boolean;
  note?: string;
  added: Array<ReleaseDiffPackage>;
  removed: Array<ReleaseDiffPackage>;
  changed: Array<ReleaseDiffPackage>;
  truncated?: boolean;
};

