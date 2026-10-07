/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RegionalRuntimeQuotaEnvelope } from './RegionalRuntimeQuotaEnvelope.js';
/**
 * At least one original envelope is present. Receipt storage is independent of frozen admission and native drain acknowledgement.
 */
export type RegionalRuntimeQuotaDeliveryReceipt = {
  grant: (RegionalRuntimeQuotaEnvelope | null);
  retirement: (RegionalRuntimeQuotaEnvelope | null);
};

