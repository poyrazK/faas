/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPProbeRPCRequest } from './MCPProbeRPCRequest.js';
/**
 * One bounded, read-only MCP JSON-RPC request to an exact replica.
 */
export type MCPInstanceProtocolProbeRequest = {
  /**
   * Deployment preview hostname and optional port.
   */
  host: string;
  /**
   * Literal absolute MCP endpoint path without query, fragment, escapes, empty interior segments, or route patterns.
   */
  endpoint: string;
  protocol_version: '2026-07-28' | '2025-11-25';
  rpc_request: MCPProbeRPCRequest;
};

