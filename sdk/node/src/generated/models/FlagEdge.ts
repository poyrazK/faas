/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { FlagEdgeRoute } from './FlagEdgeRoute.js';
/**
 * Edge routing of a variant flag: the gateway evaluates the flag for GET and HEAD requests to a routed path and rewrites the path for the decided variant, so apps without an SDK serve it. Verified customers bucket as in an SDK; anonymous visitors by a first-party cookie. Needs the edge_flags platform flag to add or change; runtime bundles leave it out.
 */
export type FlagEdge = {
  routes: Array<FlagEdgeRoute>;
};

