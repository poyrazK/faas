/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MigrationCutover } from './MigrationCutover.js';
/**
 * Latest app-owned cutover plans, newest first. Up to twenty are retained; terminal plans may be pruned after ninety days.
 */
export type MigrationCutoverList = {
  plans: Array<MigrationCutover>;
};

