/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIReplayTarget } from './OutboundAIReplayTarget.js';
/**
 * Replay a captured call on up to 5 targets (ADR-997).
 */
export type CreateOutboundAIReplayRequest = {
  targets: Array<OutboundAIReplayTarget>;
};

