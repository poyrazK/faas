/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { AgentActivityResponse } from '../models/AgentActivityResponse.js';
import type { AgentApprovalChannelsRequest } from '../models/AgentApprovalChannelsRequest.js';
import type { AgentApprovalChannelsResponse } from '../models/AgentApprovalChannelsResponse.js';
import type { AgentApprovalResponse } from '../models/AgentApprovalResponse.js';
import type { AgentApprovalsResponse } from '../models/AgentApprovalsResponse.js';
import type { AgentBudgetRequest } from '../models/AgentBudgetRequest.js';
import type { AgentBudgetResponse } from '../models/AgentBudgetResponse.js';
import type { AgentClientRequest } from '../models/AgentClientRequest.js';
import type { AgentConnectionsResponse } from '../models/AgentConnectionsResponse.js';
import type { AgentConnectionsRevokeAllResponse } from '../models/AgentConnectionsRevokeAllResponse.js';
import type { APIKeyResponse } from '../models/APIKeyResponse.js';
import type { CreateKeyRequest } from '../models/CreateKeyRequest.js';
import type { CreateOrgAPIKeyRequest } from '../models/CreateOrgAPIKeyRequest.js';
import type { GraceWindowResponse } from '../models/GraceWindowResponse.js';
import type { ListOrgAPIKeysResponse } from '../models/ListOrgAPIKeysResponse.js';
import type { RotateKeyResponse } from '../models/RotateKeyResponse.js';
import type { RotateOrgAPIKeyRequest } from '../models/RotateOrgAPIKeyRequest.js';
import type { RotateOrgAPIKeyResponse } from '../models/RotateOrgAPIKeyResponse.js';
import type { SetGraceWindowRequest } from '../models/SetGraceWindowRequest.js';
import type { CancelablePromise } from '../../generated/core/CancelablePromise.js';
import { OpenAPI } from '../../generated/core/OpenAPI.js';
import { request as __request } from '../../generated/core/request.js';
export class KeysService {
  /**
   * List API keys (no plaintext).
   * @returns APIKeyResponse API key metadata on the account. Plaintext is never returned.
   * @throws ApiError
   */
  public static listKeys(): CancelablePromise<Array<APIKeyResponse>> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/keys',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Mint a new API key.
   * Returns the plaintext key **once**. The plaintext is never stored
   * and cannot be retrieved; subsequent GETs return only the prefix.
   *
   * @returns APIKeyResponse The new API key. **The plaintext is returned exactly once** — every subsequent GET returns only the prefix.
   * @throws ApiError
   */
  public static createKey({
    requestBody,
  }: {
    /**
     * Create a new API key for the authenticated account. Plaintext is returned exactly once in the 201 response and cannot be recovered later — store it immediately. rev2 for the scope vocabulary.
     */
    requestBody: CreateKeyRequest,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/keys',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke an API key.
   * @returns void
   * @throws ApiError
   */
  public static deleteKey({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/keys/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the agents connected to the account.
   * The account's connected agents, newest first:
   * hosted MCP sign-ins, whose `mcp-login` keys the hosted MCP server
   * holds, and API keys with the `agent` scope. Only keys that still work
   * are listed. A hosted sign-in shows the client name its consent page
   * showed and the site the client was verified at, when known.
   *
   * @returns AgentConnectionsResponse The connected agents.
   * @throws ApiError
   */
  public static listAgentConnections(): CancelablePromise<AgentConnectionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-connections',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List agent approval requests.
   * With an agent connection's key: that agent's requests that needed a
   * person's approval, newest first. With any other credential: the
   * pending requests the caller may decide, those of agents they
   * connected and of agents acting in organizations they own or
   * administer. A person decides only on the review page.
   *
   * @returns AgentApprovalsResponse The approval requests.
   * @throws ApiError
   */
  public static listAgentApprovals(): CancelablePromise<AgentApprovalsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-approvals',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Show where the account's agents' approval requests also go.
   * The notification channels that get a message for each new approval
   * request of the account's agents, besides the email to the person
   * who connected the agent.
   *
   * @returns AgentApprovalChannelsResponse The account's approval channels.
   * @throws ApiError
   */
  public static getAgentApprovalChannels(): CancelablePromise<AgentApprovalChannelsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-approvals/channels',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Choose where the account's agents' approval requests also go.
   * Replaces the notification channels that get the account's agents'
   * approval requests. Name your own channels; an empty list leaves
   * email only. Needs an admin credential, so an agent cannot change
   * where its own requests go.
   *
   * @returns AgentApprovalChannelsResponse The channels now bound.
   * @throws ApiError
   */
  public static setAgentApprovalChannels({
    requestBody,
  }: {
    requestBody: AgentApprovalChannelsRequest,
  }): CancelablePromise<AgentApprovalChannelsResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/agent-approvals/channels',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Get an agent approval request.
   * An agent reads its own request to learn whether a person approved
   * it; a person reads one they may decide. Any other request answers
   * 404.
   *
   * @returns AgentApprovalResponse The approval request.
   * @throws ApiError
   */
  public static getAgentApproval({
    id,
  }: {
    /**
     * The approval request's id.
     */
    id: string,
  }): CancelablePromise<AgentApprovalResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-approvals/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke every agent connected to the account.
   * Revokes the API key of every connected agent; each must sign in
   * again. Needs an admin credential. Audits `key.revoked` with reason
   * `agent_disconnected` for each key and `agent_connections.revoke_all`
   * with the count.
   *
   * @returns AgentConnectionsRevokeAllResponse How many connections were revoked.
   * @throws ApiError
   */
  public static revokeAllAgentConnections(): CancelablePromise<AgentConnectionsRevokeAllResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/agent-connections/revoke_all',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Name the client of the calling hosted MCP key.
   * Called by the hosted MCP server after a browser sign-in, with the
   * connection's own `mcp-login` key. Any other credential is refused.
   * The first name wins.
   *
   * @returns void
   * @throws ApiError
   */
  public static nameAgentClient({
    requestBody,
  }: {
    requestBody: AgentClientRequest,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/agent-connections/self',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: validation_failed | env_var_invalid_key`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke the calling hosted MCP key.
   * Called by the hosted MCP server when a connection ends (consent
   * denied, or the connection revoked or expired), with that connection's
   * own `mcp-login` key. Any other credential is refused.
   *
   * @returns void
   * @throws ApiError
   */
  public static disconnectAgent(): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/agent-connections/self',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the calling agent's own activity.
   * The write requests (POST, PUT, PATCH, DELETE) the calling agent key
   * made in the last 7 days, newest first. Only an agent key can call it;
   * a person reads an agent's log by its connection id.
   *
   * @returns AgentActivityResponse One page of the calling agent's write requests, newest first.
   * @throws ApiError
   */
  public static agentConnectionSelfActivity({
    before,
    limit,
  }: {
    /**
     * The `next_before` value of an earlier activity page; omit it for the newest rows.
     */
    before?: string,
    /**
     * Activity rows per page, 1 to 200. Default 50.
     */
    limit?: number,
  }): CancelablePromise<AgentActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-connections/self/activity',
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the calling agent's own budget.
   * The monthly budget for always-on capacity that applies to the
   * calling agent key and what it committed this month. Only an agent key
   * can call it.
   *
   * @returns AgentBudgetResponse The calling agent's budget and spend.
   * @throws ApiError
   */
  public static agentConnectionSelfBudget(): CancelablePromise<AgentBudgetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-connections/self/budget',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a connected agent's budget.
   * The monthly budget for always-on capacity (warm instances, schedule
   * floors, prewarms) that applies to one of the account's agents, where
   * it comes from, and what the agent committed this month. Past the
   * budget, each change needs a person's approval. An agent key may read
   * only its own.
   *
   * @returns AgentBudgetResponse The agent's budget and spend.
   * @throws ApiError
   */
  public static agentConnectionBudget({
    id,
  }: {
    /**
     * API key id of the connected agent whose budget to read or set.
     */
    id: string,
  }): CancelablePromise<AgentBudgetResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-connections/{id}/budget',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Set a connected agent's budget.
   * Sets the agent's monthly budget, makes it unlimited (an organization's
   * ceiling still applies) or returns it to the plan's default: half the
   * plan's default spend cap. Needs an admin credential; an agent cannot
   * change its own budget.
   *
   * @returns AgentBudgetResponse The agent's budget after the change.
   * @throws ApiError
   */
  public static setAgentConnectionBudget({
    id,
    requestBody,
  }: {
    /**
     * API key id of the connected agent whose budget to read or set.
     */
    id: string,
    requestBody: AgentBudgetRequest,
  }): CancelablePromise<AgentBudgetResponse> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/agent-connections/{id}/budget',
      path: {
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read a connected agent's activity.
   * The write requests one of the account's agents made in the last 7
   * days, newest first: when, method, path, response status and the
   * approval request it went through on. Reads and requests the approval
   * gate answered are not listed. An agent key may read only its own.
   *
   * @returns AgentActivityResponse One page of the connected agent's write requests, newest first.
   * @throws ApiError
   */
  public static agentConnectionActivity({
    id,
    before,
    limit,
  }: {
    /**
     * API key id of the connected agent whose activity to read.
     */
    id: string,
    /**
     * The `next_before` value of an earlier activity page; omit it for the newest rows.
     */
    before?: string,
    /**
     * Activity rows per page, 1 to 200. Default 50.
     */
    limit?: number,
  }): CancelablePromise<AgentActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/agent-connections/{id}/activity',
      path: {
        'id': id,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke one connected agent.
   * Revokes the agent's API key; the agent must sign in again. Needs an
   * admin credential. Revoking it again succeeds; an id that is not one of
   * the account's connected agents is 404.
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeAgentConnection({
    id,
  }: {
    /**
     * The connected agent's API key id.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/agent-connections/{id}',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Rotate an API key.
   * Mints a new key (status='active') and demotes the old key in
   * a single transaction. The new key
   * inherits the predecessor's label + scopes so the customer's
   * CI config does not need to chase a label change.
   *
   * The old key's `expires_at` is OVERWRITTEN to the grace
   * deadline (now + grace_window_days, where grace_window_days
   * resolves from the per-account override or the 7-day plan
   * default). Status flips to 'grace' for the window, then to
   * 'revoked' at the deadline. Setting
   * `accounts.key_grace_window_days = 0` makes rotation atomic
   * (old key revoked immediately).
   *
   * Returns the new plaintext exactly once — the old plaintext
   * is not re-issued (we only store the SHA-256 hash). The
   * customer's CI script captures the new plaintext at the
   * moment of rotation and uses the new key thereafter; the old
   * key remains valid only for the grace window.
   *
   * Quota: rotation is quota-neutral (-1 +1 = 0 net). A
   * customer AT the per-account cap can still rotate.
   *
   * @returns RotateKeyResponse New key minted, old key now in 'grace' (or 'revoked' if grace_window_days=0).
   * @throws ApiError
   */
  public static rotateKey({
    id,
  }: {
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<RotateKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/keys/{id}/rotate',
      path: {
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        404: `No such key, or the key is already revoked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List the agents acting in an organization.
   * Hosted MCP connections bound to the organization and its API keys
   * with the `agent` scope that still work, newest first, with the
   * member who connected each. Owners and admins only.
   *
   * @returns AgentConnectionsResponse The organization's connected agents.
   * @throws ApiError
   */
  public static listOrgAgentConnections({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AgentConnectionsResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/agent-connections',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke every agent acting in an organization.
   * Revokes the API key of every agent acting in the organization; each
   * must sign in again. Owners and admins only. Each revocation is
   * recorded in the organization's activity.
   *
   * @returns AgentConnectionsRevokeAllResponse How many of the organization's connections were revoked.
   * @throws ApiError
   */
  public static revokeAllOrgAgentConnections({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<AgentConnectionsRevokeAllResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/agent-connections/revoke_all',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the activity of an agent acting in an organization.
   * The write requests an agent acting in the organization made in the
   * last 7 days, newest first. Owners and admins only; an id that is not
   * one of the organization's agents is 404.
   *
   * @returns AgentActivityResponse One page of the organization agent's write requests, newest first.
   * @throws ApiError
   */
  public static orgAgentConnectionActivity({
    slug,
    id,
    before,
    limit,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * API key id of the organization agent whose activity to read.
     */
    id: string,
    /**
     * The `next_before` value of an earlier activity page; omit it for the newest rows.
     */
    before?: string,
    /**
     * Activity rows per page, 1 to 200. Default 50.
     */
    limit?: number,
  }): CancelablePromise<AgentActivityResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/agent-connections/{id}/activity',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `code: bad_request — generic 400 envelope. Specific codes (missing Upload-Offset header on PATCH /v1/uploads/{id}, malformed JSON body, plan cap exceeded as \`source_too_large\`) ship as the \`code\` field.`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke one agent acting in an organization.
   * Revokes the agent's API key; the agent must sign in again. Owners and
   * admins only. An id that is not one of the organization's agents is
   * 404.
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeOrgAgentConnection({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * The API key id of an agent acting in the organization.
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}/agent-connections/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `code: not_found`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * List API keys minted against the active org.
   * Returns every key the org owns (active + grace + revoked).
   * Mirrors `GET /v1/keys`; the canonical path. The `org_id`
   * on every row will match `{slug}` because the store filters
   * server-side on the loaded membership.
   *
   * @returns ListOrgAPIKeysResponse Org-scoped API key list.
   * @throws ApiError
   */
  public static listOrgApiKeys({
    slug,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
  }): CancelablePromise<ListOrgAPIKeysResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Org slug not found, or caller has no membership.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Mint a new API key for the active org.
   * Returns the plaintext exactly once (same as `POST /v1/keys`).
   * The new row's `org_id` is the loaded membership's org; personal
   * orgs are mintable (the `org_personal_immutable` 409 applies to
   * mutations on the org row, not key mints against it).
   *
   * @returns APIKeyResponse New API key minted against the org.
   * @throws ApiError
   */
  public static createOrgApiKey({
    slug,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    requestBody: CreateOrgAPIKeyRequest,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (unknown scope, label too long).`,
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Active-org header missing or unknown; caller has no membership on the resolved slug.`,
        429: `Per-account key quota (\`api.Plan.KeysMax\`) reached.`,
      },
    });
  }
  /**
   * Fetch a single API key by id (org-scoped).
   * Lookup mirror of `GET /v1/keys/{id}`; this path is the canonical
   * single-key read. The response is the standard `APIKeyResponse` (no
   * plaintext). Cross-org probes collapse to 404.
   *
   * @returns APIKeyResponse Single API key.
   * @throws ApiError
   */
  public static getOrgApiKey({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<APIKeyResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Revoke an API key (org-scoped).
   * Soft-delete mirror of `DELETE /v1/keys/{id}`. Status flips to
   * 'revoked'; subsequent bearer-auth attempts hit `ErrAPIKeyRevoked`
   * (401 unauthenticated). The audit row carries `org_id`.
   *
   * @returns void
   * @throws ApiError
   */
  public static revokeOrgApiKey({
    slug,
    id,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
  }): CancelablePromise<void> {
    return __request(OpenAPI, {
      method: 'DELETE',
      url: '/v1/orgs/{slug}/keys/{id}',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in this org, or active-org slug not found.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Rotate an API key (org-scoped).
   * Org-scoped counterpart of `POST /v1/keys/{id}/rotate`. Mints a
   * new key (status='active') and demotes the predecessor into the
   * grace window in one transaction. The new key inherits the
   * predecessor's `org_id` — rotation never silently rebinds across
   * orgs. Quota is neutral (-1 +1 = 0).
   *
   * @returns RotateOrgAPIKeyResponse New key minted, predecessor in 'grace' (or 'revoked' if grace_window_days=0).
   * @throws ApiError
   */
  public static rotateOrgApiKey({
    slug,
    id,
    requestBody,
  }: {
    /**
     * App slug. Lowercase letters, digits, hyphens; must start and end with alnum.
     */
    slug: string,
    /**
     * 32-hex-char opaque ID (NOT canonical UUID).
     */
    id: string,
    requestBody?: RotateOrgAPIKeyRequest,
  }): CancelablePromise<RotateOrgAPIKeyResponse> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/orgs/{slug}/keys/{id}/rotate',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        401: `code: unauthorized`,
        403: `code: forbidden — caller is authenticated but lacks the required scope, OR plan_limit_trusted_signers / plan_limit_secret / etc. when the resource count would exceed the plan cap.`,
        404: `Key id not in org, or org slug not found, or predecessor already revoked.`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Read the per-account rotation grace-window override.
   * Returns the customer's current override (the value stored in
   * `accounts.key_grace_window_days`) and the plan default
   * alongside. `days=null` means "no override" — the rotation
   * handler uses the plan default (api.DefaultAPIKeyGraceWindowDays = 7).
   *
   * @returns GraceWindowResponse Current override + plan default.
   * @throws ApiError
   */
  public static getGraceWindow(): CancelablePromise<GraceWindowResponse> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/account/keys/grace_window_days',
      errors: {
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
      },
    });
  }
  /**
   * Set the per-account rotation grace-window override.
   * Writes the override and invalidates the in-process cache so
   * the next rotation observes the new value. `days=0` is atomic
   * rotation (no grace); `days=null` (or omission) clears the
   * override and falls back to the plan default.
   * A failed save returns retryable 503 without invalidating the cache
   * or emitting a successful change audit event.
   *
   * @returns GraceWindowResponse Override applied.
   * @throws ApiError
   */
  public static setGraceWindow({
    requestBody,
  }: {
    requestBody: SetGraceWindowRequest,
  }): CancelablePromise<GraceWindowResponse> {
    return __request(OpenAPI, {
      method: 'PATCH',
      url: '/v1/account/keys/grace_window_days',
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid body (negative days, or more than 30).`,
        401: `code: unauthorized`,
        429: `429 application/problem+json response. Authentication throttling uses
        \`auth_rate_limited\`; plan and usage limits use their specific stable
        codes such as \`plan_limit_concurrency\`, \`quota_exhausted\` and
        \`profile_investigation_limit\`.
        `,
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
