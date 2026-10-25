/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { EdgeRuleHeaderOp } from './EdgeRuleHeaderOp.js';
/**
 * Mutates request + response headers. The gateway enforces a hard-coded blacklist (Host, Content-Length, Transfer-Encoding, Connection, x-faas-*).
 */
export type EdgeRuleHeadersAction = {
  request_headers?: Array<EdgeRuleHeaderOp>;
  response_headers?: Array<EdgeRuleHeaderOp>;
};

