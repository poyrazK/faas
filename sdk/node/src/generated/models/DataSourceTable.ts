/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataConnectionIssue } from './DataConnectionIssue.js';
import type { DataSourceColumn } from './DataSourceColumn.js';
/**
 * A discovered public table. Unsupported tables remain visible with reasons and cannot be selected for replication.
 */
export type DataSourceTable = {
  /**
   * A valid explicit column selection may omit incompatible non-key columns; all table-level checks still apply.
   */
  column_selection_supported?: boolean;
  name: string;
  supported: boolean;
  columns: Array<DataSourceColumn>;
  issues: Array<DataConnectionIssue>;
};

