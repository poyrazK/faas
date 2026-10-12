/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPriceEntry } from './OutboundAIPriceEntry.js';
/**
 * The price list in effect; effective_from is null when none was ever set, and cost is then zero.
 */
export type OutboundAIPriceListResponse = {
  effective_from: string | null;
  entries: Array<OutboundAIPriceEntry>;
};

