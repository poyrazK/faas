/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { APISandboxExampleRequest } from './APISandboxExampleRequest.js';
export type APISandboxScenarioGuide = {
  name: string;
  /**
   * Synthetic IDs shown to invite recipients.
   */
  seeded_ids?: Record<string, string>;
  requests: Array<APISandboxExampleRequest>;
};

