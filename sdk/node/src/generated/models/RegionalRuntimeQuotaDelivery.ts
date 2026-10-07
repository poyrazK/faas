/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { RegionalRuntimeQuotaBinding } from './RegionalRuntimeQuotaBinding.js';
import type { RegionalRuntimeQuotaEnvelope } from './RegionalRuntimeQuotaEnvelope.js';
/**
 * Canonical delivery body bounded to 20480 bytes. The direct grant and retirement routes reject consent_id; consent-based delivery uses the delegated receiver.
 */
export type RegionalRuntimeQuotaDelivery = {
  binding: RegionalRuntimeQuotaBinding;
  authority: RegionalRuntimeQuotaEnvelope;
  consent_id?: string;
};

