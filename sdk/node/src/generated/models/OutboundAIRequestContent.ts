/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIContentBody } from './OutboundAIContentBody.js';
/**
 * One logged AI call's captured bodies as the gateway sent them to the provider and read them back; never headers (ADR-996).
 */
export type OutboundAIRequestContent = {
  request_id: string;
  created_at: string;
  request: OutboundAIContentBody;
  response: OutboundAIContentBody;
};

