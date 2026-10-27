/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
import type { CancelMCPReadinessMaintenanceRequest } from '../models/CancelMCPReadinessMaintenanceRequest.js';
import type { CreateMCPReadinessMaintenanceRequest } from '../models/CreateMCPReadinessMaintenanceRequest.js';
import type { MCPInstanceHealthProbeResponse } from '../models/MCPInstanceHealthProbeResponse.js';
import type { MCPInstanceProtocolProbeRequest } from '../models/MCPInstanceProtocolProbeRequest.js';
import type { MCPInstanceProtocolProbeResponse } from '../models/MCPInstanceProtocolProbeResponse.js';
import type { MCPReadinessAttentionAction } from '../models/MCPReadinessAttentionAction.js';
import type { MCPReadinessAttentionHistory } from '../models/MCPReadinessAttentionHistory.js';
import type { MCPReadinessAttentionPage } from '../models/MCPReadinessAttentionPage.js';
import type { MCPReadinessAttentionReceipt } from '../models/MCPReadinessAttentionReceipt.js';
import type { MCPReadinessFallbackPolicy } from '../models/MCPReadinessFallbackPolicy.js';
import type { MCPReadinessMaintenancePage } from '../models/MCPReadinessMaintenancePage.js';
import type { MCPReadinessMaintenanceWindow } from '../models/MCPReadinessMaintenanceWindow.js';
import type { MCPReadinessNotificationAction } from '../models/MCPReadinessNotificationAction.js';
import type { MCPReadinessNotificationQueues } from '../models/MCPReadinessNotificationQueues.js';
import type { MCPReadinessNotificationReceipt } from '../models/MCPReadinessNotificationReceipt.js';
import type { MCPReadinessPagerDutyAcknowledgementEvents } from '../models/MCPReadinessPagerDutyAcknowledgementEvents.js';
import type { MCPReadinessPagerDutyAcknowledgementPolicy } from '../models/MCPReadinessPagerDutyAcknowledgementPolicy.js';
import type { MCPReadinessPagerDutyPolicy } from '../models/MCPReadinessPagerDutyPolicy.js';
import type { MCPReadinessReminderDefaults } from '../models/MCPReadinessReminderDefaults.js';
import type { MCPTaskCompatibilityReportRequest } from '../models/MCPTaskCompatibilityReportRequest.js';
import type { MCPTaskCompatibilityReportResponse } from '../models/MCPTaskCompatibilityReportResponse.js';
import type { SetMCPReadinessFallbackRequest } from '../models/SetMCPReadinessFallbackRequest.js';
import type { SetMCPReadinessPagerDutyAcknowledgementsRequest } from '../models/SetMCPReadinessPagerDutyAcknowledgementsRequest.js';
import type { SetMCPReadinessPagerDutyRequest } from '../models/SetMCPReadinessPagerDutyRequest.js';
import type { SetMCPReadinessReminderDefaultsRequest } from '../models/SetMCPReadinessReminderDefaultsRequest.js';
import type { CancelablePromise } from '../core/CancelablePromise.js';
import { OpenAPI } from '../core/OpenAPI.js';
import { request as __request } from '../core/request.js';
export class McpService {
  /**
   * Read masked PagerDuty webhook acknowledgement settings
   * Read scope and MFA required. Version zero is disabled. The stable relative webhook path is returned before configuration; concatenate it with the trusted API origin when creating the PagerDuty v3 subscription.
   * @returns MCPReadinessPagerDutyAcknowledgementPolicy Current masked acknowledgement webhook binding
   * @throws ApiError
   */
  public static getMcpReadinessPagerDutyAcknowledgements({
    slug,
    id,
    destination,
  }: {
    /**
     * Application owning the PagerDuty acknowledgement subscription.
     */
    slug: string,
    /**
     * Readiness rule receiving signed PagerDuty acknowledgements.
     */
    id: string,
    /**
     * Select an independent primary or fallback binding. Omit to read the binding owning the original callback URL.
     */
    destination?: 'primary' | 'fallback',
  }): CancelablePromise<MCPReadinessPagerDutyAcknowledgementPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'destination': destination,
      },
      errors: {
        400: `Invalid getMCPReadinessPagerDutyAcknowledgements parameters or body`,
        401: `Authentication required for getMCPReadinessPagerDutyAcknowledgements`,
        403: `Insufficient scope or MFA for getMCPReadinessPagerDutyAcknowledgements`,
        404: `No owned rule for getMCPReadinessPagerDutyAcknowledgements`,
      },
    });
  }
  /**
   * Configure signed PagerDuty acknowledgement sync
   * Admin scope and MFA required. Enable with the selected primary or fallback native PagerDuty version, service ID and write-only v3 subscription signing secret. Destination defaults to primary; fallback requires expected_fallback_version instead of expected_pagerduty_version and matches only incidents with activated fallback. The server binds the native receiver fingerprint and region. Only the supplied current signing secret is accepted; use a fresh secret to rotate credentials. Each destination has independent secrets and policy versions. Saving one leaves the other unchanged. Disabling clears only the selected destination credentials immediately; when destination is omitted on disable, the original callback binding is selected. Signed acknowledgements stop reminders only for a matching pinned incident. Optional sync_unacknowledgements accepts signed incident.unacknowledged events. Reminders resume within the existing interval and budget only after every tracked provider acknowledgement clears. Existing untracked or manual acknowledgements require local release. Monitoring and recovery remain driven by readiness probes.
   * @returns MCPReadinessPagerDutyAcknowledgementPolicy Saved acknowledgement webhook binding with its new version
   * @throws ApiError
   */
  public static setMcpReadinessPagerDutyAcknowledgements({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application owning the PagerDuty acknowledgement subscription.
     */
    slug: string,
    /**
     * Readiness rule receiving signed PagerDuty acknowledgements.
     */
    id: string,
    /**
     * Complete replacement of the signed acknowledgement binding and credential.
     */
    requestBody: SetMCPReadinessPagerDutyAcknowledgementsRequest,
  }): CancelablePromise<MCPReadinessPagerDutyAcknowledgementPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid setMCPReadinessPagerDutyAcknowledgements parameters or body`,
        401: `Authentication required for setMCPReadinessPagerDutyAcknowledgements`,
        403: `Insufficient scope or MFA for setMCPReadinessPagerDutyAcknowledgements`,
        404: `No owned rule for setMCPReadinessPagerDutyAcknowledgements`,
        409: `Webhook policy or selected receiver version changed; reload both bindings`,
      },
    });
  }
  /**
   * Inspect PagerDuty acknowledgement event receipts
   * Read scope and MFA required. Bounded immutable provider decisions ordered by receipt time and ID; raw webhook bodies, signatures, credentials and actor details are never retained.
   * @returns MCPReadinessPagerDutyAcknowledgementEvents Retained provider acknowledgement event page
   * @throws ApiError
   */
  public static listMcpReadinessPagerDutyAcknowledgementEvents({
    slug,
    id,
    before,
    limit = 20,
  }: {
    /**
     * Application owning PagerDuty acknowledgement receipts.
     */
    slug: string,
    /**
     * Readiness rule whose provider acknowledgement history is listed.
     */
    id: string,
    /**
     * Exclusive opaque cursor from the preceding provider event page.
     */
    before?: string,
    /**
     * Maximum provider event receipts in this page.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessPagerDutyAcknowledgementEvents> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty-acknowledgements/events',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `Invalid listMCPReadinessPagerDutyAcknowledgementEvents parameters or body`,
        401: `Authentication required for listMCPReadinessPagerDutyAcknowledgementEvents`,
        403: `Insufficient scope or MFA for listMCPReadinessPagerDutyAcknowledgementEvents`,
        404: `No owned rule for listMCPReadinessPagerDutyAcknowledgementEvents`,
      },
    });
  }
  /**
   * Inspect masked PagerDuty readiness configuration
   * Read scope and MFA required. Version zero is unconfigured and uses webhook primary. Integration keys and fingerprints are never returned. Only owned application readiness webhook rules are supported, including disabled rules.
   * @returns MCPReadinessPagerDutyPolicy Current masked PagerDuty primary receiver policy
   * @throws ApiError
   */
  public static getMcpReadinessPagerDuty({
    slug,
    id,
  }: {
    /**
     * Application owning the native PagerDuty configuration.
     */
    slug: string,
    /**
     * Readiness webhook rule whose selected native PagerDuty receiver is configured.
     */
    id: string,
  }): CancelablePromise<MCPReadinessPagerDutyPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `Invalid PagerDuty rule identifier or unsupported query parameters`,
        401: `Authenticate to read native PagerDuty settings`,
        403: `Native PagerDuty reads require read scope and MFA`,
        404: `No owned readiness rule matches the PagerDuty configuration request`,
      },
    });
  }
  /**
   * Configure native PagerDuty primary delivery
   * Admin scope and MFA required. Enable using a write-only 32-character Events API v2 integration key for the selected US or EU endpoint (US by default). Configuration is pinned at an incident's first delivery attempt; changes and disabling affect unattempted incidents. Existing pinned receivers continue receiving recovery. Webhook and distinct native PagerDuty fallback remain available. Keys are encrypted with a dedicated namespace. Upgrade all readiness evaluators before enabling. PagerDuty acceptance does not confirm paging. A separate signed acknowledgement webhook policy can synchronize operator acknowledgement.
   * @returns MCPReadinessPagerDutyPolicy Updated version and masked PagerDuty integration configuration
   * @throws ApiError
   */
  public static setMcpReadinessPagerDuty({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application owning the native PagerDuty configuration.
     */
    slug: string,
    /**
     * Readiness webhook rule whose selected native PagerDuty receiver is configured.
     */
    id: string,
    /**
     * Complete PagerDuty configuration replacement fenced by its current version.
     */
    requestBody: SetMCPReadinessPagerDutyRequest,
  }): CancelablePromise<MCPReadinessPagerDutyPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-pagerduty',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid PagerDuty key or versioned replacement settings`,
        401: `Authenticate before replacing the native PagerDuty receiver`,
        403: `PagerDuty configuration writes require admin scope and MFA`,
        404: `Requested PagerDuty rule does not belong to this application`,
        409: `PagerDuty policy version changed; reload its current configuration`,
      },
    });
  }
  /**
   * Read rule reminder defaults
   * Read scope and MFA required. Returns version zero and a disabled policy when unconfigured. Current settings apply only when an incident opens; existing incidents retain their captured policy and overrides. Only MCP readiness webhook rules support defaults.
   * @returns MCPReadinessReminderDefaults Current reminder defaults for future incidents
   * @throws ApiError
   */
  public static getMcpReadinessReminderDefaults({
    slug,
    id,
  }: {
    /**
     * Application owning the reminder defaults rule.
     */
    slug: string,
    /**
     * Readiness webhook rule whose future incidents inherit reminders.
     */
    id: string,
  }): CancelablePromise<MCPReadinessReminderDefaults> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-reminder-defaults',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `Invalid reminder defaults rule or query parameters`,
        401: `Authenticate to inspect reminder defaults`,
        403: `Reminder defaults inspection requires read scope and MFA`,
        404: `Reminder defaults rule was not found in this application`,
      },
    });
  }
  /**
   * Replace reminder defaults for future incidents
   * Admin scope and MFA required. Expected version fences configuration changes; zero is valid when unconfigured. A zero interval and zero budget disable defaults. Enabled policies use a 60–86400 second interval and 1–10 lifetime reminders per incident. Changes never alter existing incidents or reset their reminder budget. New incidents capture the policy and version atomically when they open. Upgrade all readiness evaluators before enabling defaults.
   * @returns MCPReadinessReminderDefaults Versioned replacement of rule reminder defaults
   * @throws ApiError
   */
  public static setMcpReadinessReminderDefaults({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application owning the reminder defaults rule.
     */
    slug: string,
    /**
     * Readiness webhook rule whose future incidents inherit reminders.
     */
    id: string,
    /**
     * Current defaults version and complete replacement reminder policy.
     */
    requestBody: SetMCPReadinessReminderDefaultsRequest,
  }): CancelablePromise<MCPReadinessReminderDefaults> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-reminder-defaults',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid defaults interval or lifetime reminder budget`,
        401: `Authenticate before replacing reminder defaults`,
        403: `Reminder defaults changes require admin scope and MFA`,
        404: `Target reminder defaults rule is not owned by this application`,
        409: `Reminder defaults version changed; refresh the current configuration`,
      },
    });
  }
  /**
   * Inspect a readiness fallback destination policy
   * Read scope and MFA required. Returns version zero and disabled when unconfigured. Signing credentials are masked. The policy applies when an incident first claims delivery; existing incident receiver URLs stay pinned.
   * @returns MCPReadinessFallbackPolicy Current masked readiness fallback policy
   * @throws ApiError
   */
  public static getMcpReadinessFallback({
    slug,
    id,
  }: {
    /**
     * Application that owns the readiness webhook rule.
     */
    slug: string,
    /**
     * Readiness webhook alert rule UUID.
     */
    id: string,
  }): CancelablePromise<MCPReadinessFallbackPolicy> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback',
      path: {
        'slug': slug,
        'id': id,
      },
      errors: {
        400: `Invalid fallback rule identifier or unsupported rule family`,
        401: `Authenticate to inspect a fallback destination`,
        403: `Fallback inspection requires read scope and MFA`,
        404: `No owned application and readiness rule match this fallback lookup`,
      },
    });
  }
  /**
   * Replace a readiness fallback destination policy
   * Admin scope and MFA required. expected_version fences concurrent edits. Enable selects webhook (default) with a distinct public HTTP(S) destination and signing secret, or PagerDuty with a separate integration key and optional US/EU region. Primary and fallback cannot select the same PagerDuty integration. Threshold counts completed failed primary dispatch claims per logical event. Changes apply to incidents not yet attempted; a new webhook secret for the same URL also supplies ongoing pinned deliveries. PagerDuty provider, region and integration identity stay pinned; credential re-encryption for the identical integration refreshes ongoing deliveries. Disabling affects new incidents; disable the alert rule to pause existing deliveries. Upgrade all evaluators before enabling fallback.
   * @returns MCPReadinessFallbackPolicy Replaced fallback policy with its incremented version
   * @throws ApiError
   */
  public static setMcpReadinessFallback({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application that owns the readiness webhook rule.
     */
    slug: string,
    /**
     * Readiness webhook alert rule UUID.
     */
    id: string,
    requestBody: SetMCPReadinessFallbackRequest,
  }): CancelablePromise<MCPReadinessFallbackPolicy> {
    return __request(OpenAPI, {
      method: 'PUT',
      url: '/v1/apps/{slug}/alerts/{id}/mcp-readiness-fallback',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid fallback destination or failure threshold`,
        401: `Authenticate to change readiness fallback delivery`,
        403: `Changing fallback delivery requires admin scope and MFA`,
        404: `Application or rule for the fallback change is not owned`,
        409: `Fallback policy version changed; refresh before replacing it`,
        503: `Fallback secret sealing or persistence is unavailable`,
      },
    });
  }
  /**
   * List incident acknowledgement and reminder state
   * Read scope and MFA required. Lists open incidents by default in ascending incident UUID order. Environment filters include all-environment incidents. State is separate from readiness and notification delivery. The next reminder time is eligibility only; fresh matching unhealthy evidence and no outstanding notification for this incident are required.
   * @returns MCPReadinessAttentionPage Current attention state from a consistent snapshot
   * @throws ApiError
   */
  public static listMcpReadinessAttention({
    slug,
    scope,
    after,
    limit = 20,
    includeResolved = false,
  }: {
    /**
     * Application owning the readiness incidents.
     */
    slug: string,
    /**
     * Environment; omission includes every environment.
     */
    scope?: string,
    /**
     * Incident UUID from next_after on the previous page.
     */
    after?: string,
    /**
     * Maximum number of incidents in this page.
     */
    limit?: number,
    /**
     * Include resolved incidents and their retained attention state.
     */
    includeResolved?: boolean,
  }): CancelablePromise<MCPReadinessAttentionPage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-attention',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'after': after,
        'limit': limit,
        'include_resolved': includeResolved,
      },
      errors: {
        400: `Invalid incident attention filters or cursor`,
        401: `Authenticate to inspect incident attention`,
        403: `Incident attention inspection requires read scope and MFA`,
        404: `Application owning the incident attention was not found`,
      },
    });
  }
  /**
   * Read audited incident attention decisions
   * Read scope and MFA required. Immutable decisions ordered by creation time and request UUID descending. History remains available after resolution. Ordinary evaluator observations and reminder creation do not add operator decisions.
   * @returns MCPReadinessAttentionHistory Retained incident attention decisions
   * @throws ApiError
   */
  public static listMcpReadinessAttentionHistory({
    slug,
    id,
    before,
    limit = 20,
  }: {
    /**
     * Application owning this incident.
     */
    slug: string,
    /**
     * Readiness incident UUID.
     */
    id: string,
    /**
     * Opaque next_before cursor from the previous history page.
     */
    before?: string,
    /**
     * Maximum number of retained attention decisions in this page.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessAttentionHistory> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-incidents/{id}/attention-actions',
      path: {
        'slug': slug,
        'id': id,
      },
      query: {
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `Invalid incident attention history cursor`,
        401: `Authenticate to read incident attention history`,
        403: `Incident attention history requires read scope and MFA`,
        404: `Attention history incident was not found in this application`,
      },
    });
  }
  /**
   * Acknowledge an incident or configure finite reminders
   * Admin scope and MFA required. New decisions require an open readiness webhook incident and its current attention version. Disabled rules may be configured but cannot create or deliver reminders. Identical app-scoped request UUID retries return their durable receipt even after expiry or recovery; changed requests conflict. Acknowledgement only prevents reminder creation and suppresses unleased reminders. In-flight delivery may finish. Initial failure and recovery notifications retain their behavior. Reminders inherit optional rule defaults at incident opening and may be overridden per incident. Their lifetime creation budget never resets. Configure zero interval and zero budget to disable. Absolute acknowledgement expiry must be in the future and no more than 30 days away; omission acknowledges indefinitely. Native paging and fallback destinations are not configured here.
   * @returns MCPReadinessAttentionReceipt Original durable incident attention receipt
   * @throws ApiError
   */
  public static actMcpReadinessAttention({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application owning this incident.
     */
    slug: string,
    /**
     * Readiness incident UUID.
     */
    id: string,
    /**
     * Version-fenced decision with a stable idempotency UUID and retained reason.
     */
    requestBody: MCPReadinessAttentionAction,
  }): CancelablePromise<MCPReadinessAttentionReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-readiness-incidents/{id}/attention-actions',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid acknowledgement expiry or reminder policy`,
        401: `Authenticate before changing incident attention`,
        403: `Incident attention decisions require admin scope and MFA`,
        404: `Incident for this attention decision was not found`,
        409: `Incident resolved or version or idempotency request changed`,
      },
    });
  }
  /**
   * Inspect readiness notification queue health
   * Read scope and MFA required. Returns a consistent snapshot ordered by rule UUID. Environment filters include all-environment rules with their whole queue so a blocking head is visible. Counts exclude delivered, suppressed and abandoned events. Maintenance is reported without mutating queued events.
   * @returns MCPReadinessNotificationQueues Snapshot of readiness notification delivery queues
   * @throws ApiError
   */
  public static listMcpReadinessNotificationQueues({
    slug,
    scope,
    after,
    limit = 20,
  }: {
    /**
     * Application whose readiness notification queues are inspected.
     */
    slug: string,
    /**
     * Environment filter; omitted includes every rule and all-environment rules always remain visible.
     */
    scope?: string,
    /**
     * Rule UUID returned as next_after on the preceding queue page.
     */
    after?: string,
    /**
     * Maximum readiness notification queues per page.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessNotificationQueues> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-notifications',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'after': after,
        'limit': limit,
      },
      errors: {
        400: `Invalid notification queue environment or rule cursor`,
        401: `Authenticate to inspect readiness notification delivery`,
        403: `Queue inspection requires read scope and completed MFA`,
        404: `Application for notification queue inspection was not found`,
      },
    });
  }
  /**
   * Retry or abandon a readiness receiver or event
   * Admin scope and MFA required. With destination supplied the action changes only that receiver head and preserves other receivers. Omit destination to act on the oldest whole event. Retry makes a failed or expired-lease head eligible on the next evaluator pass. Abandon permanently stops the selected deliveries while retaining incident history. Actions recheck the selected queue order and lease plus rule enablement and maintenance. The shared event version fences stale requests. Reusing a request ID with the identical payload including destination returns the durable receipt; changed payload conflicts. A receipt confirms the decision rather than webhook delivery.
   * @returns MCPReadinessNotificationReceipt Original durable notification recovery receipt
   * @throws ApiError
   */
  public static actMcpReadinessNotification({
    slug,
    requestBody,
  }: {
    /**
     * Application owning the notification recovery decision.
     */
    slug: string,
    /**
     * Stable request ID, current head version and retained operator reason.
     */
    requestBody: MCPReadinessNotificationAction,
  }): CancelablePromise<MCPReadinessNotificationReceipt> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-readiness-notifications/actions',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid notification recovery request or operator reason`,
        401: `Authenticate before requesting notification recovery`,
        403: `Notification recovery requires admin scope and completed MFA`,
        404: `Recovery event does not belong to the requested application`,
        409: `Queue head changed or recovery is prevented by its current state`,
      },
    });
  }
  /**
   * List readiness maintenance windows for an environment
   * Newest created first, including expired and cancelled windows. Read scope and MFA required.
   * @returns MCPReadinessMaintenancePage Maintenance window history
   * @throws ApiError
   */
  public static listMcpReadinessMaintenance({
    slug,
    scope = 'default',
    before,
    limit = 20,
  }: {
    /**
     * Application owning the maintenance window history.
     */
    slug: string,
    /**
     * Exact environment whose maintenance windows are listed.
     */
    scope?: string,
    /**
     * Opaque maintenance-history cursor returned as next_before.
     */
    before?: string,
    /**
     * Maximum number of maintenance windows in the response.
     */
    limit?: number,
  }): CancelablePromise<MCPReadinessMaintenancePage> {
    return __request(OpenAPI, {
      method: 'GET',
      url: '/v1/apps/{slug}/mcp-readiness-maintenance',
      path: {
        'slug': slug,
      },
      query: {
        'scope': scope,
        'before': before,
        'limit': limit,
      },
      errors: {
        400: `Invalid maintenance environment or history cursor`,
        401: `Authenticate to read maintenance window history`,
        403: `Maintenance history requires read scope and MFA`,
        404: `Application for maintenance history was not found`,
      },
    });
  }
  /**
   * Schedule maintenance or temporarily silence readiness notifications
   * Admin scope and MFA required. Monitoring and incident recording continue. Windows match exact environments and end automatically. An omitted start begins now; duration is at most 720 hours. Notifications already claimed may complete.
   * @returns MCPReadinessMaintenanceWindow Scheduled maintenance window
   * @throws ApiError
   */
  public static createMcpReadinessMaintenance({
    slug,
    requestBody,
  }: {
    /**
     * Application owning the maintenance window history.
     */
    slug: string,
    /**
     * Environment, finite time interval and reason for notification muting.
     */
    requestBody: CreateMCPReadinessMaintenanceRequest,
  }): CancelablePromise<MCPReadinessMaintenanceWindow> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-readiness-maintenance',
      path: {
        'slug': slug,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid maintenance start, end, environment or reason`,
        401: `Authenticate before scheduling a maintenance window`,
        403: `Scheduling maintenance requires admin scope and MFA`,
        404: `Application for the new maintenance window was not found`,
      },
    });
  }
  /**
   * Cancel a readiness maintenance window
   * Admin scope and MFA required. Cancellation preserves the window and original cancellation attribution. Other overlapping windows remain effective.
   * @returns MCPReadinessMaintenanceWindow Cancelled window, including attribution
   * @throws ApiError
   */
  public static cancelMcpReadinessMaintenance({
    slug,
    id,
    requestBody,
  }: {
    /**
     * Application owning the window to cancel.
     */
    slug: string,
    /**
     * UUID of the retained maintenance window.
     */
    id: string,
    /**
     * Reason retained with the original cancellation attribution.
     */
    requestBody: CancelMCPReadinessMaintenanceRequest,
  }): CancelablePromise<MCPReadinessMaintenanceWindow> {
    return __request(OpenAPI, {
      method: 'POST',
      url: '/v1/apps/{slug}/mcp-readiness-maintenance/{id}/cancel',
      path: {
        'slug': slug,
        'id': id,
      },
      body: requestBody,
      mediaType: 'application/json',
      errors: {
        400: `Invalid maintenance window ID or cancellation reason`,
        401: `Authenticate before cancelling a maintenance window`,
        403: `Cancelling maintenance requires admin scope and MFA`,
        404: `Maintenance window does not belong to the requested application`,
        409: `The maintenance window ended before cancellation`,
      },
    });
  }
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
    /**
     * Deployment UUID containing the exact replica selected for the MCP health probe.
     */
    deployment: string,
    /**
     * Running instance UUID targeted by the MCP health probe.
     */
    instance: string,
    /**
     * Virtual-host authority used for this replica health request, including an optional port.
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
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
   * @returns MCPInstanceProtocolProbeResponse Bounded MCP protocol response from the verified running replica.
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
    /**
     * Deployment UUID containing the exact replica selected for the MCP protocol probe.
     */
    deployment: string,
    /**
     * Running instance UUID targeted by the MCP protocol probe.
     */
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
        503: `Generic 503 envelope. Used by the API capacity gate (e.g.
        host age recipient not loaded → registry credential PUT
        returns 503 instead of accepting plaintext).
        `,
      },
    });
  }
}
