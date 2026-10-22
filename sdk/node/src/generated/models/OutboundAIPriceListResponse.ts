/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPriceEntry } from './OutboundAIPriceEntry.js';
/**
 * The price list in effect; effective_from is null when none was ever set. Models the list does not name are priced from the built-in catalog.
 */
export type OutboundAIPriceListResponse = {
  effective_from: string | null;
  entries: Array<OutboundAIPriceEntry>;
};

