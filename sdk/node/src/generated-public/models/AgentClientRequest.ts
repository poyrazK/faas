/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * The client a hosted MCP connection's key belongs to, named once by the hosted MCP server.
 */
export type AgentClientRequest = {
  /**
   * Printable client name, without control characters or surrounding spaces.
   */
  client_name: string;
  /**
   * Lower-case host name the client was verified at.
   */
  client_site?: string;
};

