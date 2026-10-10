/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { PublishEventResponse } from './PublishEventResponse.js';
/**
 * Durable app producer-key publication decision.
 */
export type AppPublishEventResponse = {
  app_id: string;
  /**
   * Stable app.UUID subscription source.
   */
  source: string;
  duplicate: boolean;
  receipt: PublishEventResponse;
};

