/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { MCPInstanceHealthProbeResponse } from '../models/MCPInstanceHealthProbeResponse.js';
import type { MCPInstanceProtocolProbeRequest } from '../models/MCPInstanceProtocolProbeRequest.js';
import type { MCPInstanceProtocolProbeResponse } from '../models/MCPInstanceProtocolProbeResponse.js';
import type { MCPTaskCompatibilityReportRequest } from '../models/MCPTaskCompatibilityReportRequest.js';
import type { MCPTaskCompatibilityReportResponse } from '../models/MCPTaskCompatibilityReportResponse.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class McpService {
  /**
   * Publish an MCP task handler compatibility snapshot
   * Replaces the app's bounded observer snapshot with aggregate waiting-task and live-worker counts by tool and handler version. The report contains no task IDs, owners, or payloads; timestamps are assigned by Gregale.
   * @returns void
   * @throws ApiError
   */
  public static putMcpTaskCompatibilityReport({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: MCPTaskCompatibilityReportRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/mcp-task-compatibility',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
        402: `code: billing_past_due — account is suspended; pay invoice to resume.`,
        404: `code: not_found`,
        413: `Request body exceeds 128 KiB.`,
        422: `code: validation_failed | source_invalid | build_undetected | handler_missing | image_required | cron_invalid | secret_invalid_key`,
      },
    });
  }
  /**
   * Read the latest MCP task handler compatibility snapshot
   * Returns the latest bounded observer snapshot, including server-computed freshness. An unpublished snapshot is returned as published=false and stale=true.
   * @returns MCPTaskCompatibilityReportResponse Latest snapshot or an explicit unpublished/stale response.
   * @throws ApiError
   */
  public static getMcpTaskCompatibilityReport({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<MCPTaskCompatibilityReportResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-task-compatibility',
      path: {
        'slug': slug,
      },
      errors: {
        404: `code: not_found`,
      },
    });
  }
  /**
   * Probe MCP catalog health on one exact replica.
   * Requires app read scope and MFA. The platform validates that the
   * deployment belongs to the app and the instance is RUNNING in that
   * deployment, then forwards a GET to the fixed `/healthz` path on that
   * instance. The response includes platform-stamped app, deployment, and
   * instance IDs; workload response headers cannot override them. The
   * supplied host is only the virtual-host authority delivered to the
   * workload and cannot contain a path or URL credentials.
   *
   * @returns MCPInstanceHealthProbeResponse Exact instance response and platform-selected identity.
   * @throws ApiError
   */
  public static probeMcpInstanceHealth({
    slug,
    deployment,
    instance,
    host,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    deployment: string,
    instance: string,
    /**
     * Deployment preview hostname and optional port.
     */
    host: string,
  }): CancelablePromise<MCPInstanceHealthProbeResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/deployments/{deployment}/instances/{instance}/mcp-health',
      path: {
        'slug': slug,
        'deployment': deployment,
        'instance': instance,
      },
      query: {
        'host': host,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
  /**
   * Send one read-only MCP protocol request to an exact replica.
   * Requires app read scope and MFA. The platform verifies that the
   * instance is RUNNING in the requested deployment, then forwards one
   * allowlisted MCP initialize or catalog-list request to the supplied
   * literal endpoint path on that exact instance. Tool execution, resource
   * reads, prompt rendering, and task operations are rejected. The response
   * includes platform-stamped identity and a bounded workload response.
   * The optional MCP-Probe-Authorization header carries a workload bearer
   * token for this request only; it is not logged or stored.
   *
   * @returns MCPInstanceProtocolProbeResponse Exact instance response and platform-selected identity.
   * @throws ApiError
   */
  public static probeMcpInstanceProtocol({
    slug,
    deployment,
    instance,
    requestBody,
    mcpProbeAuthorization,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    deployment: string,
    instance: string,
    requestBody: MCPInstanceProtocolProbeRequest,
    /**
     * Optional bearer token forwarded to the MCP workload. Never returned or persisted.
     */
    mcpProbeAuthorization?: string,
  }): CancelablePromise<MCPInstanceProtocolProbeResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/deployments/{deployment}/instances/{instance}/mcp-probe',
      path: {
        'slug': slug,
        'deployment': deployment,
        'instance': instance,
      },
      headers: {
        'MCP-Probe-Authorization': mcpProbeAuthorization,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        404: `code: not_found`,
        409: `code: conflict`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the apid capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
