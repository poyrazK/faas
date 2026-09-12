from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_slo_response_window import AppSLOResponseWindow, check_app_slo_response_window

if TYPE_CHECKING:
    from ..models.slo_duration import SLODuration


T = TypeVar("T", bound="AppSLOResponse")


@_attrs_define
class AppSLOResponse:
    """Per-app SLO panel returned by `GET /v1/apps/{slug}/slo?window=`
    (issue #696 / ADR-082). Distinct from `AppMetricsResponse`
    (issue #273 / ADR-042): the SLO surface is a fixed-window
    (1h/24h/7d) summary of the customer-facing SLO signals,
    not a 5m slice for the dashboard. The fields overlap only
    on latency percentiles, error rate, and cold-boot rate — the
    remaining fields (`throttled_total`, `instance_hours`,
    `gb_hours`) are net-new per the issue. `wake_queue_p95_ms`
    remains in the wire shape for compatibility and is zero until
    the underlying histogram has an app label.

    On Prometheus failure the endpoint returns 200 with
    zeroed fields and `source: "degraded: <reason>"`. When
    Postgres is down but the PromQL pass succeeded, only
    `instance_hours` / `gb_hours` are zeroed and `source` is
    `"degraded: postgres unavailable"`.

    """

    app_id: str
    app_slug: str
    window: AppSLOResponseWindow
    """Echoed SLO window, e.g. `24h`."""
    source: str
    """"prometheus" on success; "degraded: <reason>" otherwise. Per-app shape: when Postgres fails only
    instance_hours/gb_hours are zeroed."""
    as_of: datetime.datetime
    """RFC3339Nano UTC stamp at which the SLO panel was assembled."""
    request_duration: SLODuration
    """Shared latency sub-shape used by `AppSLOResponse` and
    `AccountSLOResponse`. Three percentiles over the SLO
    window (2xx class only); NaN/Inf from `histogram_quantile`
    on an empty window is coerced to 0 by the handler.
    """
    error_rate_pct: float
    """Share of [45]xx requests in the window for this app."""
    cold_boot_rate_pct: float
    """Share of requests that triggered a cold boot."""
    instance_hours: float
    """Sum of instance × minute / 60 over the window (from `usage_minutes`)."""
    gb_hours: float
    """Sum of mb_seconds / 3600 / 1024 over the window (from `usage_minutes`)."""
    wake_queue_p95_ms: float
    """Reserved compatibility field. Zero until the wake-queue histogram can be scoped to this app."""
    requests_total: int
    throttled_total: int
    """Per-app rate-limit count over the window."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        app_slug = self.app_slug

        window: str = self.window

        source = self.source

        as_of = self.as_of.isoformat()

        request_duration = self.request_duration.to_dict()

        error_rate_pct = self.error_rate_pct

        cold_boot_rate_pct = self.cold_boot_rate_pct

        instance_hours = self.instance_hours

        gb_hours = self.gb_hours

        wake_queue_p95_ms = self.wake_queue_p95_ms

        requests_total = self.requests_total

        throttled_total = self.throttled_total

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "app_slug": app_slug,
                "window": window,
                "source": source,
                "as_of": as_of,
                "request_duration": request_duration,
                "error_rate_pct": error_rate_pct,
                "cold_boot_rate_pct": cold_boot_rate_pct,
                "instance_hours": instance_hours,
                "gb_hours": gb_hours,
                "wake_queue_p95_ms": wake_queue_p95_ms,
                "requests_total": requests_total,
                "throttled_total": throttled_total,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.slo_duration import SLODuration

        d = dict(src_dict)
        app_id = d.pop("app_id")

        app_slug = d.pop("app_slug")

        window = check_app_slo_response_window(d.pop("window"))

        source = d.pop("source")

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        request_duration = SLODuration.from_dict(d.pop("request_duration"))

        error_rate_pct = d.pop("error_rate_pct")

        cold_boot_rate_pct = d.pop("cold_boot_rate_pct")

        instance_hours = d.pop("instance_hours")

        gb_hours = d.pop("gb_hours")

        wake_queue_p95_ms = d.pop("wake_queue_p95_ms")

        requests_total = d.pop("requests_total")

        throttled_total = d.pop("throttled_total")

        app_slo_response = cls(
            app_id=app_id,
            app_slug=app_slug,
            window=window,
            source=source,
            as_of=as_of,
            request_duration=request_duration,
            error_rate_pct=error_rate_pct,
            cold_boot_rate_pct=cold_boot_rate_pct,
            instance_hours=instance_hours,
            gb_hours=gb_hours,
            wake_queue_p95_ms=wake_queue_p95_ms,
            requests_total=requests_total,
            throttled_total=throttled_total,
        )

        app_slo_response.additional_properties = d
        return app_slo_response

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
