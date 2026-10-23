/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { DataAPIChangeEvent } from './DataAPIChangeEvent.js';
/**
 * A runtime's private publication of recorded changes, in recorded order.
 */
export type DataAPIChangeEventBatch = {
  version: 1;
  events: Array<DataAPIChangeEvent>;
};

