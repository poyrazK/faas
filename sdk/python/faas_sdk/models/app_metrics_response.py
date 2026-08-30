from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_metrics_response_range import AppMetricsResponseRange, check_app_metrics_response_range
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_row import RouteRow


T = TypeVar("T", bound="AppMetricsResponse")


@_attrs_define
class AppMetricsResponse:
    """Per-app metrics snapshot (issue #273 / ADR-041). Latencies are
    milliseconds for the 2xx class only; failures surface as
    `error_rate_pct`. `wake_p95_ms` is the FLEET p95 — the
    underlying `gateway_wake_latency_seconds` histogram is
    unlabeled. On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: "degraded: <reason>"`.

    """

    app_id: str
    range_: AppMetricsResponseRange
    """Echoed window."""
    source: str
    """"prometheus" on success, "degraded: <reason>" otherwise."""
    as_of: datetime.datetime
    """RFC3339Nano UTC."""
    request_count: int
    """Share of requests in the window. Drives the dashboard empty state."""
    latency_p50_ms: float
    """p50 of `gateway_request_duration_seconds_bucket{class="2xx"}` over the window, in ms."""
    latency_p95_ms: float
    """p95 over 2xx traffic in the window, in ms."""
    latency_p99_ms: float
    """p99 over 2xx traffic in the window, in ms."""
    error_rate_pct: float
    """Share of [45]xx requests in the window."""
    cold_start_pct: float
    """Share of requests that triggered a cold boot (the WakeGate
    leader). Followers waiting on the gate see zero cold
    contribution; their wait is on the §12 dashboard via
    `gateway_wake_queue_wait_seconds`.
    """
    wake_p95_ms: float
    """FLEET p95 wake latency (the unlabeled histogram). Labelled as such in the UI."""
    queue_depth: int | Unset = UNSET
    """Current wake-queue depth
    (`gateway_queue_depth{app, account_id}` — `account_id`
    admitted via accountLabelSet, overflow=`__other__`,
    added in PR-D). Backs the `queue_backlog_growing`
    alert preset (ADR-123): comparison `gt 50` over the
    chosen window. Best-effort: absent on Prometheus
    failure (the field is `null`); the evaluator's
    degraded-source contract skips firing rather than
    guessing.
    """
    egress_bytes: int | Unset = UNSET
    """Per-app egress byte delta over the window (informational; not billed). ADR-046. Source:
    schedd_egress_net_tx_bytes_total{app} (Prom rollup of usage_minutes.net_tx_bytes — PR-2 wires the rollup; until
    then this field stays 0)."""
    tx_bytes: int | Unset = UNSET
    """Per-app egress byte delta over the window, gateway-side mirror (informational; not billed). ADR-046 PR-2 /
    issue #415 PR-2. Source: gateway_egress_tx_bytes_total{app} (Prom rollup; the gatewayd-internal-local per-
    instance egress ring populates the counter on each raw-stream chunk). Distinct from `egress_bytes` (the schedd-
    side mirror) so a divergence between the two surfaces immediately on the dashboard. Best-effort: query failure
    does NOT flip the response to degraded — matches the `egress_bytes` semantics."""
    routes: list[RouteRow] | Unset = UNSET
    """Per-route breakdown for opt-in apps (ADR-093). Absent when
    `route_metrics_enabled` is false on the app — the dashboard
    distinguishes "feature off" (field absent) from "feature
    on, no traffic" (empty array). Each row is the bounded
    detail from the gatewayd-internal in-memory reader: max
    50 distinct routes + the `__route_other__` wildcard-path
    overflow bucket. The route label is method + raw path
    (pre-rewrite, ADR-093 D6).
    """
    wakes_24h: int | Unset = UNSET
    """Count of `wake.boot_started` events the schedd recorded for
    this app in the trailing 24 hours. Sourced from the
    `events` table via the events_wake_id_idx jsonb expression
    index (migration 00114) — sub-second on a healthy app.
    Best-effort: 0 on a degraded store call, an empty app, or
    when the events table predates the post-ADR-123 schema
    (pre-PR-A boot_started rows carry no app_id field, so the
    cast returns NULL which COUNT(*) coerces to 0 — same
    posture as the wake-timeline view's `WakeCountWithMeta`
    denominator at cmd/apid/handlers_dashboard.go:2659). The
    dashboard surfaces this as the "wakes today" line item;
    combined with `cold_start_pct` it answers "is my app
    wake-bound or sleep-bound". Field absent on Free
    (the per-app dashboard is Hobby+ only — see
    pkg/api/limits.go::PerAppMetricsAllowed).
    """
    cache_hit_rate_pct: float | Unset = UNSET
    """Share of cache-eligible requests served from
    `gateway_response_cache` (ADR-122) over the window.
    ALWAYS present on the wire (the SDK can rely on the
    documented schema) — 0 means either "feature off" (no
    cache rule attached) or "feature on, zero traffic". The
    dashboard distinguishes the two via the existence of the
    `routes` block, not via field absence. The field stays 0
    until the response-cache consumer-facing metric lands
    (the current per-app dashboard surfaces the
    `gateway_response_cache_total{outcome=hit/miss}` rollup
    through the operator-side §12 panel, not this field).
    """
    error_budget_pct: float | Unset = UNSET
    """Trailing-30d API-availability error budget remaining (0 =
    exhausted, 100 = full). ALWAYS present on the wire so the
    SDK can rely on the documented schema — 0 renders as "—"
    on the dashboard rather than a misleading "budget
    exhausted" message. Computed against the plan's
    API-availability SLO target (99.5% per spec §12). The
    field stays 0 until the per-plan SLO target lands on the
    `Limits` struct (issue TBD); once wired, the field is
    computed against `apid_request_total{account_id, code}`
    over the trailing 30d, scaled by the per-plan SLO
    target.
    """
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        range_: str = self.range_

        source = self.source

        as_of = self.as_of.isoformat()

        request_count = self.request_count

        latency_p50_ms = self.latency_p50_ms

        latency_p95_ms = self.latency_p95_ms

        latency_p99_ms = self.latency_p99_ms

        error_rate_pct = self.error_rate_pct

        cold_start_pct = self.cold_start_pct

        wake_p95_ms = self.wake_p95_ms

        queue_depth = self.queue_depth

        egress_bytes = self.egress_bytes

        tx_bytes = self.tx_bytes

        routes: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.routes, Unset):
            routes = []
            for routes_item_data in self.routes:
                routes_item = routes_item_data.to_dict()
                routes.append(routes_item)

        wakes_24h = self.wakes_24h

        cache_hit_rate_pct = self.cache_hit_rate_pct

        error_budget_pct = self.error_budget_pct

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "range": range_,
                "source": source,
                "as_of": as_of,
                "request_count": request_count,
                "latency_p50_ms": latency_p50_ms,
                "latency_p95_ms": latency_p95_ms,
                "latency_p99_ms": latency_p99_ms,
                "error_rate_pct": error_rate_pct,
                "cold_start_pct": cold_start_pct,
                "wake_p95_ms": wake_p95_ms,
            }
        )
        if queue_depth is not UNSET:
            field_dict["queue_depth"] = queue_depth
        if egress_bytes is not UNSET:
            field_dict["egress_bytes"] = egress_bytes
        if tx_bytes is not UNSET:
            field_dict["tx_bytes"] = tx_bytes
        if routes is not UNSET:
            field_dict["routes"] = routes
        if wakes_24h is not UNSET:
            field_dict["wakes_24h"] = wakes_24h
        if cache_hit_rate_pct is not UNSET:
            field_dict["cache_hit_rate_pct"] = cache_hit_rate_pct
        if error_budget_pct is not UNSET:
            field_dict["error_budget_pct"] = error_budget_pct

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_row import RouteRow

        d = dict(src_dict)
        app_id = d.pop("app_id")

        range_ = check_app_metrics_response_range(d.pop("range"))

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        request_count = d.pop("request_count")

        latency_p50_ms = d.pop("latency_p50_ms")

        latency_p95_ms = d.pop("latency_p95_ms")

        latency_p99_ms = d.pop("latency_p99_ms")

        error_rate_pct = d.pop("error_rate_pct")

        cold_start_pct = d.pop("cold_start_pct")

        wake_p95_ms = d.pop("wake_p95_ms")

        queue_depth = d.pop("queue_depth", UNSET)

        egress_bytes = d.pop("egress_bytes", UNSET)

        tx_bytes = d.pop("tx_bytes", UNSET)

        _routes = d.pop("routes", UNSET)
        routes: list[RouteRow] | Unset = UNSET
        if _routes is not UNSET:
            routes = []
            for routes_item_data in _routes:
                routes_item = RouteRow.from_dict(routes_item_data)

                routes.append(routes_item)

        wakes_24h = d.pop("wakes_24h", UNSET)

        cache_hit_rate_pct = d.pop("cache_hit_rate_pct", UNSET)

        error_budget_pct = d.pop("error_budget_pct", UNSET)

        app_metrics_response = cls(
            app_id=app_id,
            range_=range_,
            source=source,
            as_of=as_of,
            request_count=request_count,
            latency_p50_ms=latency_p50_ms,
            latency_p95_ms=latency_p95_ms,
            latency_p99_ms=latency_p99_ms,
            error_rate_pct=error_rate_pct,
            cold_start_pct=cold_start_pct,
            wake_p95_ms=wake_p95_ms,
            queue_depth=queue_depth,
            egress_bytes=egress_bytes,
            tx_bytes=tx_bytes,
            routes=routes,
            wakes_24h=wakes_24h,
            cache_hit_rate_pct=cache_hit_rate_pct,
            error_budget_pct=error_budget_pct,
        )

        app_metrics_response.additional_properties = d
        return app_metrics_response

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
