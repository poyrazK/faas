/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ConsumerCreditPack } from './ConsumerCreditPack.js';
/**
 * The packs offered by an app and the billing page where buyers return.
 */
export type ConsumerCreditPackSettings = {
  /**
   * The HTTPS billing page to return to, required when packs are offered.
   */
  return_url?: string;
  packs: Array<ConsumerCreditPack>;
};

