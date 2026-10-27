/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ReleaseDiffCode } from './ReleaseDiffCode.js';
import type { ReleaseDiffConfig } from './ReleaseDiffConfig.js';
import type { ReleaseDiffDatabase } from './ReleaseDiffDatabase.js';
import type { ReleaseDiffDependencies } from './ReleaseDiffDependencies.js';
import type { ReleaseDiffField } from './ReleaseDiffField.js';
import type { ReleaseDiffSide } from './ReleaseDiffSide.js';
/**
 * What changed between two releases of one app.
 */
export type ReleaseDiff = {
  app: string;
  from?: ReleaseDiffSide;
  to: ReleaseDiffSide;
  /**
   * One line, such as "new code, 2 config keys, 5 packages".
   */
  summary: string;
  code: ReleaseDiffCode;
  config: ReleaseDiffConfig;
  dependencies: ReleaseDiffDependencies;
  runtime: Array<ReleaseDiffField>;
  database: ReleaseDiffDatabase;
};

