/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIPriceEntry } from './OutboundAIPriceEntry.js';
/**
 * One provider's built-in AI price catalog (ADR-994), generated from LiteLLM's MIT-licensed price data. model_prefix holds the catalog model name.
 */
export type OutboundAIPriceCatalogResponse = {
  ai_provider: string;
  /**
   * URL of the price data the catalog was generated from.
   */
  source: string;
  generated: string;
  /**
   * USD per EUR used for the conversion, as a decimal string.
   */
  usd_per_eur: string;
  /**
   * Source and date of the exchange rate.
   */
  fx_source: string;
  entries: Array<OutboundAIPriceEntry>;
};

