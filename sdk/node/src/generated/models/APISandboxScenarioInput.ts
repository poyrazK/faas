/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxOutboundWebhook } from './APISandboxOutboundWebhook.js';
import type { APISandboxScenarioGuide } from './APISandboxScenarioGuide.js';
import type { APISandboxStripeWebhook } from './APISandboxStripeWebhook.js';
/**
 * Editable scenario content, including fixtures and request expectations.
 */
export type APISandboxScenarioInput = (APISandboxScenarioGuide & {
  /**
   * Synthetic fixture data of up to 8 KiB. Passed to the sandbox release as GREGALE_API_SANDBOX_SEED.
   */
  seed: Record<string, any>;
  stripe_webhook?: APISandboxStripeWebhook;
  outbound_webhook?: APISandboxOutboundWebhook;
});

