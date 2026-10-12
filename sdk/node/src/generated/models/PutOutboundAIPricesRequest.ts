/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPriceEntry } from './OutboundAIPriceEntry.js';
/**
 * Replace an integration's price list. It takes effect immediately; earlier lists stay for costs already settled.
 */
export type PutOutboundAIPricesRequest = {
  entries: Array<OutboundAIPriceEntry>;
};

