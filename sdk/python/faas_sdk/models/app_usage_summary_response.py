from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_usage_summary_response_source import (
    AppUsageSummaryResponseSource,
    check_app_usage_summary_response_source,
)

T = TypeVar("T", bound="AppUsageSummaryResponse")


@_attrs_define
class AppUsageSummaryResponse:
    """Wire shape for `GET /v1/apps/{slug}/usage?since=&until=`
    (commit 4 of the per-app observability PR series).
    Plan-gated Hobby+; Free falls through with
    `plan_app_usage_summary_not_allowed`.

    `gb_hours` is the rounded float of `mb_seconds / 1024 /
    3600` (mirror of `meter.MonthlyUsageGB`'s 6-decimal
    rounding). `overage_gb_hours = max(0, gb_hours -
    plan_included_gb_hours)` — 0 when the customer is under
    their included band, the billable overage above the band
    otherwise. The Stripe pusher bills overage at €0.01/GB-h
    (CLAUDE.md integer-cents-only rule).

    """

    slug: str
    period_start: datetime.datetime
    """Half-open [period_start, period_end) window's inclusive lower bound. UTC."""
    period_end: datetime.datetime
    """Half-open window's exclusive upper bound. UTC midnight snap (handler defaults)."""
    mb_seconds: int
    """Sum of usage_minutes.mb_seconds for this app in the window (ADR-048 billable unit)."""
    gb_hours: float
    """mb_seconds / 1024 / 3600, rounded to 6 decimal places (mirror of meter.MonthlyUsageGB)."""
    requests: int
    """Cumulative HTTP request count (informational; not billed)."""
    tx_bytes: int
    """Cumulative HTTP response body bytes (ADR-046; diagnostic subset of net_tx_bytes; never add both)."""
    net_tx_bytes: int
    """Canonical cumulative host-interface egress bytes for the window (ADR-046; includes framing)."""
    builder_seconds: float
    """Cumulative builder-microVM CPU-seconds (informational; surfaced as a sidebar line on the dashboard)."""
    cold_boot_count: int
    """WAKE_RESTORE→WAKE_COLD_BOOT transitions in the window."""
    plan_included_gb_hours: float
    """Echoed from acct.Plan.PlanIncludedGBHours() — plan's per-month included band so the dashboard renders the
    included-band badge without a second round-trip."""
    overage_gb_hours: float
    """max(0, gb_hours - plan_included_gb_hours). 0 when the customer is under their included band; the billable
    overage above the band otherwise."""
    source: AppUsageSummaryResponseSource
    """Which rollup reader produced this summary. usage_minutes today (30d retention); usage_daily lands with the
    trail-period follow-up."""
    as_of: datetime.datetime
    """RFC3339Nano UTC stamping the envelope's authoritative 'as of' instant."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        mb_seconds = self.mb_seconds

        gb_hours = self.gb_hours

        requests = self.requests

        tx_bytes = self.tx_bytes

        net_tx_bytes = self.net_tx_bytes

        builder_seconds = self.builder_seconds

        cold_boot_count = self.cold_boot_count

        plan_included_gb_hours = self.plan_included_gb_hours

        overage_gb_hours = self.overage_gb_hours

        source: str = self.source

        as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "period_start": period_start,
                "period_end": period_end,
                "mb_seconds": mb_seconds,
                "gb_hours": gb_hours,
                "requests": requests,
                "tx_bytes": tx_bytes,
                "net_tx_bytes": net_tx_bytes,
                "builder_seconds": builder_seconds,
                "cold_boot_count": cold_boot_count,
                "plan_included_gb_hours": plan_included_gb_hours,
                "overage_gb_hours": overage_gb_hours,
                "source": source,
                "as_of": as_of,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        slug = d.pop("slug")

        period_start = datetime.datetime.fromisoformat(d.pop("period_start"))

        period_end = datetime.datetime.fromisoformat(d.pop("period_end"))

        mb_seconds = d.pop("mb_seconds")

        gb_hours = d.pop("gb_hours")

        requests = d.pop("requests")

        tx_bytes = d.pop("tx_bytes")

        net_tx_bytes = d.pop("net_tx_bytes")

        builder_seconds = d.pop("builder_seconds")

        cold_boot_count = d.pop("cold_boot_count")

        plan_included_gb_hours = d.pop("plan_included_gb_hours")

        overage_gb_hours = d.pop("overage_gb_hours")

        source = check_app_usage_summary_response_source(d.pop("source"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        app_usage_summary_response = cls(
            slug=slug,
            period_start=period_start,
            period_end=period_end,
            mb_seconds=mb_seconds,
            gb_hours=gb_hours,
            requests=requests,
            tx_bytes=tx_bytes,
            net_tx_bytes=net_tx_bytes,
            builder_seconds=builder_seconds,
            cold_boot_count=cold_boot_count,
            plan_included_gb_hours=plan_included_gb_hours,
            overage_gb_hours=overage_gb_hours,
            source=source,
            as_of=as_of,
        )

        app_usage_summary_response.additional_properties = d
        return app_usage_summary_response

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
