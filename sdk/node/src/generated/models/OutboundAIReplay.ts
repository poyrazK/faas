/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { OutboundAIReplayResult } from './OutboundAIReplayResult.js';
import type { OutboundAIReplaySource } from './OutboundAIReplaySource.js';
import type { OutboundAIReplayTarget } from './OutboundAIReplayTarget.js';
/**
 * A replay of one captured call and, once done, its results in target order.
 */
export type OutboundAIReplay = {
  id: string;
  integration_id: string;
  request_id: string;
  state: 'pending' | 'running' | 'done' | 'failed';
  error_code: string | null;
  created_at: string;
  finished_at: string | null;
  targets: Array<OutboundAIReplayTarget>;
  source: OutboundAIReplaySource;
  results: Array<OutboundAIReplayResult>;
};

