/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Explicit one-call consent with a canonical argument digest and optional opaque credential binding; no argument bodies or credentials.
 */
export type ToolInvocationIntent = {
  version: string;
  name: string;
  arguments_sha256: string;
  access_mode: 'open' | 'external-oauth';
  /**
   * Local token-variable hash or exact owner-granted private binding; never a token hash or value
   */
  credential_binding_sha256?: string;
  timeout_seconds: number;
  allow_tool_execution: boolean;
};

