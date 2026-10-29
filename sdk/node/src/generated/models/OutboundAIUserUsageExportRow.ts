/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIUserUsageItem } from './OutboundAIUserUsageItem.js';
/**
 * One NDJSON line of a per-end-user usage export; CSV rows carry the same fields in this order.
 */
export type OutboundAIUserUsageExportRow = ({
  usage_date: string;
} & OutboundAIUserUsageItem);

