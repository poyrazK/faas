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
   * ISO 4217 currency of every catalog price: the billing currency, USD. Absent from responses of releases before ADR 20261019123822.
   */
  currency?: string;
  /**
   * Deprecated: always 1, since the catalog is in US dollars.
   * @deprecated
   */
  usd_per_eur?: string;
  /**
   * Deprecated: names no exchange rate.
   * @deprecated
   */
  fx_source?: string;
  entries: Array<OutboundAIPriceEntry>;
};

