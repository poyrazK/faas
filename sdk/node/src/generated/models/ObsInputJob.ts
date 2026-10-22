/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsInputExpectation } from './ObsInputExpectation.js';
import type { ObsInputMetric } from './ObsInputMetric.js';
import type { ObsInputTargets } from './ObsInputTargets.js';
/**
 * One selected job's independent expectation, scrape and gauge evidence.
 */
export type ObsInputJob = {
  job: 'apid' | 'gatewayd' | 'schedd' | 'gatewayd-internal' | 'schedd-compute' | 'vmmd';
  state: 'healthy' | 'degraded' | 'partial' | 'unknown';
  expectation: ObsInputExpectation;
  targets: ObsInputTargets;
  metrics: Array<ObsInputMetric>;
};

