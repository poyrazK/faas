/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { ObsRuntimeAppTaskClassName } from './ObsRuntimeAppTaskClassName.js';
import type { ObsRuntimeAppTaskKindName } from './ObsRuntimeAppTaskKindName.js';
/**
 * Replica-view counts for one ordered kind/class pair. These describe views of shared fleet data, not unique tasks or completion acknowledgements.
 */
export type ObsRuntimeAppTaskClassCounts = {
  kind: ObsRuntimeAppTaskKindName;
  class: ObsRuntimeAppTaskClassName;
  nonempty_views: number;
  empty_views: number;
  unknown_age_views: number;
};

