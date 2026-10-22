/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseDiffKey } from './ReleaseDiffKey.js';
/**
 * How the configuration each release started with differs, by key.
 */
export type ReleaseDiffConfig = {
  /**
   * False when the retained config history no longer reaches one of the releases.
   */
  known: boolean;
  note?: string;
  variables: Array<ReleaseDiffKey>;
  secrets: Array<ReleaseDiffKey>;
};

