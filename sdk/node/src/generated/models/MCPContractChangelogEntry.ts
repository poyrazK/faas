/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * One tool contract change on a public listing: changed tool names, never schemas.
 */
export type MCPContractChangelogEntry = {
  date: string;
  verdict: 'compatible' | 'review' | 'breaking' | 'unverified';
  tools: Array<string>;
  changes: number;
};

