/* generated using openapi-typescript-codegen -- do not edit */
/* istanbul ignore file */
/* tslint:disable */
/* eslint-disable */
/**
 * Per-app streaming classification (ADR-102 D6). The probe
 * returns the same `Streaming-Status` enum value the
 * gatewayd handler would stamp on the response header for a
 * representative request to this app, plus the effective
 * response-body cap and the per-gate flags that produced it.
 *
 * `status` values (closed enum, see `pkg/api/limits.go`):
 *
 * - `streaming` — request will stream. `effective_cap_bytes`
 * is the cap `capWriter` will enforce.
 * - `accept-json-downgrade` — pre-D3 customers with
 * `Accept: application/json` would have been buffered.
 * Post-D3 (this PR), the request streams regardless of
 * Accept; the enum variant survives one cycle so pinned
 * SDKs can grep for it.
 * - `flag-disabled` — `streaming_enabled=false` on the app.
 * - `plan-disallows` — plan tier forbids `streaming_enabled=true`;
 * CreateApp (D5) already returns 403 on this combination,
 * so this value should be unreachable on a properly-validated
 * app.
 * - `operator-disabled` — operator opt-in env is off; visible
 * only on the gatewayd side, not this probe.
 * - `upgrade-bypass` — request is an HTTP/1.1 Upgrade (e.g.
 * WebSocket) and bypasses the streaming path.
 *
 * `effective_cap_bytes` is the plan cap (`cap_kind="plan"`) when
 * no route shape is supplied. A route-aware probe may return
 * `cap_kind="endpoint-rule"` when the compiled gatewayd rule
 * matches; gatewayd failures and non-matches fall back to the
 * plan cap. A customer who needs the canonical live status still
 * fires a real request and reads the `Streaming-Status` response
 * header.
 *
 */
export type AppStreamingStatus = {
  app_id: string;
  status: 'streaming' | 'accept-json-downgrade' | 'flag-disabled' | 'plan-disallows' | 'operator-disabled' | 'upgrade-bypass';
  effective_cap_bytes: number;
  plan_cap_bytes: number;
  flag_enabled: boolean;
  plan_allowed: boolean;
  /**
   * `plan` is the plan-level fallback, `endpoint-rule` is a
   * matching compiled gatewayd response cap, and `none` is
   * reserved for a future explicit no-cap result.
   *
   */
  cap_kind?: 'plan' | 'endpoint-rule' | 'none';
};

