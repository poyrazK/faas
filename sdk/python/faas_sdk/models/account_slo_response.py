from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.account_slo_response_window import AccountSLOResponseWindow, check_account_slo_response_window

if TYPE_CHECKING:
    from ..models.slo_duration import SLODuration


T = TypeVar("T", bound="AccountSLOResponse")


@_attrs_define
class AccountSLOResponse:
    """Flat account-wide SLO rollup returned by
    `GET /v1/account/slo?window=` (issue #696 / ADR-082).
    Mirrors `AppSLOResponse` field-for-field except for
    `app_id` / `app_slug` (the rollup is account-wide). The
    fields are scalar sums/rates across the account; per-app
    drill-down is served by the existing `/v1/apps/metrics`
    endpoint.

    `source` follows the same `"degraded: <reason>"` contract
    as the per-app endpoint.

    """

    window: AccountSLOResponseWindow
    source: str
    """"prometheus" on success; "degraded: <reason>" otherwise. Account-wide shape: when Postgres fails only
    instance_hours/gb_hours are zeroed."""
    as_of: datetime.datetime
    request_duration: SLODuration
    """Shared latency sub-shape used by `AppSLOResponse` and
    `AccountSLOResponse`. Three percentiles over the SLO
    window (2xx class only); NaN/Inf from `histogram_quantile`
    on an empty window is coerced to 0 by the handler.
    """
    error_rate_pct: float
    cold_boot_rate_pct: float
    instance_hours: float
    """Sum of instance-minutes / 60 across all apps for the account."""
    gb_hours: float
    """Sum of mb_seconds / 3600 / 1024 across all apps for the account."""
    wake_queue_p95_ms: float
    """Reserved compatibility field. Zero until the wake-queue histogram can be scoped to the account's apps."""
    requests_total: int
    throttled_total: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
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
        window = check_account_slo_response_window(d.pop("window"))

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

        account_slo_response = cls(
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

        account_slo_response.additional_properties = d
        return account_slo_response

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
