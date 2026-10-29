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
    """Per-app metrics snapshot. Latencies are
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
    """Share of 5xx requests in the window. Client-caused 4xx responses do not consume availability budget."""
    cold_start_pct: float
    """Share of requests that triggered a cold boot (the WakeGate
    leader). Followers waiting on the gate see zero cold
    contribution; their wait is on the dashboard via
    `gateway_wake_queue_wait_seconds`.
    """
    wake_p95_ms: float
    """FLEET p95 wake latency (the unlabeled histogram). Labelled as such in the UI."""
    wake_samples: int | Unset = UNSET
    """Number of fleet wakes wake_p95_ms was computed from. Below 20 the p95 is not meaningful and clients show it
    as unavailable. Omitted when the count is unknown."""
    queue_depth: int | Unset = UNSET
    """Current wake-queue depth
    (`gateway_queue_depth{app, account_id}` — `account_id`
    admitted via accountLabelSet, overflow=`__other__`,
    added). Backs the `queue_backlog_growing`
    alert preset: comparison `gt 50` over the
    chosen window. Best-effort: absent on Prometheus
    failure (the field is `null`); the evaluator's
    degraded-source contract skips firing rather than
    guessing.
    """
    egress_bytes: int | Unset = UNSET
    """Per-app egress byte delta over the window (informational; not billed). It is not collected yet and stays 0."""
    tx_bytes: int | Unset = UNSET
    """Per-app egress byte delta over the window, gateway-side mirror (informational; not billed). Source:
    gateway_egress_tx_bytes_total{app} (Prom rollup; the gateway-local per-instance egress ring populates the
    counter on each raw-stream chunk). Distinct from `egress_bytes` (the scheduler-side mirror) so a divergence
    between the two surfaces immediately on the dashboard. Best-effort: query failure does NOT flip the response to
    degraded — matches the `egress_bytes` semantics."""
    routes: list[RouteRow] | Unset = UNSET
    """Per-route breakdown for opt-in apps. Absent when
    `route_metrics_enabled` is false on the app — the dashboard
    distinguishes "feature off" (field absent) from "feature
    on, no traffic" (empty array). Each row is the bounded
    detail from the gateway in-memory reader: max
    50 distinct routes + the `__route_other__` wildcard-path
    overflow bucket. The route label is method + declared template
    when available, otherwise a best-effort inferred public path.
    """
    wakes_24h: int | Unset = UNSET
    """Count of `wake.boot_started` events the scheduler recorded for
    this app in the trailing 24 hours. Sourced from the
    `events` table via the events_wake_id_idx jsonb expression
    index — sub-second on a healthy app.
    Best-effort: 0 on a degraded store call, an empty app, or
    when the events table predates per-app wake events
    (older wake events carry no app ID and count as 0). The
    dashboard surfaces this as the "wakes today" line item;
    combined with `cold_start_pct` it answers "is my app
    wake-bound or sleep-bound". Field absent on Free
    (the per-app dashboard is Hobby+ only —
    """
    cache_hit_rate_pct: float | Unset = UNSET
    """Share of cache-eligible requests served from
    `gateway_response_cache` over the selected
    window. The value is sourced from the additive
    `gateway_response_cache_app_total{app,outcome}` metric
    and counts only `hit / (hit + miss)`. Absence means the
    app had no cache telemetry in the window, not an observed
    0% hit rate.
    """
    error_budget_pct: float | Unset = UNSET
    """API-availability error budget remaining over the selected
    metrics window, using the current 99.5% API-availability
    target. 100 means the full budget remains and 0 means it is
    exhausted. Absence means the app had no requests in the
    window, not an observed exhausted budget.
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

        wake_samples = self.wake_samples

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
        if wake_samples is not UNSET:
            field_dict["wake_samples"] = wake_samples
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

        wake_samples = d.pop("wake_samples", UNSET)

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
            wake_samples=wake_samples,
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
