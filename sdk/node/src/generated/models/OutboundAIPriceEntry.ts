/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Price for model names starting with model_prefix ("*" matches any model; the longest prefix wins), in millicents per million tokens. Absent cached or cache-write prices use the input price.
 */
export type OutboundAIPriceEntry = {
  model_prefix: string;
  input_millicents_per_mtok: number;
  cached_input_millicents_per_mtok?: number;
  cache_write_millicents_per_mtok?: number;
  output_millicents_per_mtok: number;
};

