/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ProfileAttributionQuality } from './ProfileAttributionQuality.js';
import type { ProfileCoverage } from './ProfileCoverage.js';
import type { ProfileFunction } from './ProfileFunction.js';
import type { ProfileQuery } from './ProfileQuery.js';
import type { ProfileRouteCPU } from './ProfileRouteCPU.js';
import type { ProfileSource } from './ProfileSource.js';
import type { ProfileStack } from './ProfileStack.js';
/**
 * CPU profile with an explicit absence-of-samples flag.
 */
export type ProfileResponse = {
  readonly attribution?: ProfileAttributionQuality;
  routes?: Array<ProfileRouteCPU>;
  /**
   * All observed request-route labels have CPU attribution and a complete bounded request summary. This does not establish telemetry delivery or instrumentation completeness.
   */
  route_requests_complete?: boolean;
  query: ProfileQuery;
  cpu_seconds: number;
  /**
   * Number of nonzero aggregated stack records, not raw sampling ticks.
   */
  stack_count: number;
  functions: Array<ProfileFunction>;
  flamegraph: ProfileStack;
  empty: boolean;
  coverage?: ProfileCoverage;
  source?: ProfileSource;
};

