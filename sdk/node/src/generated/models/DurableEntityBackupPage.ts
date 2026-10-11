/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DurableEntityBackupInfo } from './DurableEntityBackupInfo.js';
/**
 * Bounded page of retained backup metadata with an optional continuation cursor.
 */
export type DurableEntityBackupPage = {
  items: Array<DurableEntityBackupInfo>;
  next_cursor?: string;
};

