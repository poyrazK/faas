/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { Incident } from './Incident.js';
/**
 * The account's open incidents and those resolved since `since`, open first.
 */
export type IncidentsResponse = {
  since: string;
  incidents: Array<Incident>;
  truncated: boolean;
  unavailable_sources: Array<string>;
};

