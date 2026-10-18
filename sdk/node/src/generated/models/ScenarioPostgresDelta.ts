/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Inserted and deleted selected-row multisets; updates appear as deletion and insertion.
 */
export type ScenarioPostgresDelta = {
  table: string;
  inserted: Array<Record<string, any>>;
  deleted: Array<Record<string, any>>;
};

