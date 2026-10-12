/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Bounded MCP response from one exact replica with platform-stamped identity.
 */
export type MCPInstanceProtocolProbeResponse = {
  app_id: string;
  deployment_id: string;
  instance_id: string;
  http_status: number;
  content_type?: string;
  protocol_version?: string;
  session_id?: string;
  /**
   * True when the workload returned an MCP-Session-Id header, including when its value exceeded the response header cap.
   */
  session_id_present?: boolean;
  body: string;
  body_too_large?: boolean;
};

