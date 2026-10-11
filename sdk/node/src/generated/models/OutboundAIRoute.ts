/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIRouteTarget } from './OutboundAIRouteTarget.js';
/**
 * An ordered list of same-format managed AI integrations. Apps call $GREGALE_OUTBOUND_URL/r/{id}/{provider path} (ADR-983).
 */
export type OutboundAIRoute = {
  id: string;
  name: string;
  targets: Array<OutboundAIRouteTarget>;
  created_at: string;
  updated_at: string;
};

