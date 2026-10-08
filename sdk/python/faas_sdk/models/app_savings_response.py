from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_savings_response_source import AppSavingsResponseSource, check_app_savings_response_source

T = TypeVar("T", bound="AppSavingsResponse")


@_attrs_define
class AppSavingsResponse:
    """Wire shape for `GET /v1/apps/{slug}/savings?since=&until=`.
    A scale-to-zero savings estimate: billed RAM-time compared
    with an always-on counterfactual of `baseline_instances`
    instances of `billable_ram_mb` running from `baseline_start`
    to `period_end`. `saved_* = max(0, always_on_* - actual_*)`.
    Money fields are integer millicents at
    `price_millicents_per_gb_hour` (the plan overage rate). This
    is an estimate, not an invoice line; `methodology` carries
    the sentence clients print under the figure.

    """

    slug: str
    period_start: datetime.datetime
    """Inclusive lower bound of the resolved window, UTC midnight, never earlier than period_end - 30d."""
    period_end: datetime.datetime
    """Exclusive upper bound of the resolved window, UTC midnight."""
    baseline_start: datetime.datetime
    """Start of the always-on counterfactual: the later of period_start and the app's first billed hour. Equals
    period_end when the app billed nothing."""
    baseline_instances: int
    """max(min_instances, 1) — the always-on instance floor."""
    billable_ram_mb: int
    """Per-instance plan RAM + per-VM overhead used for the counterfactual. Companion sidecars are excluded, so the
    estimate is conservative."""
    always_on_mb_seconds: int
    actual_mb_seconds: int
    """Billed mb_seconds in the window (same source as /usage)."""
    saved_mb_seconds: int
    always_on_gb_hours: float
    actual_gb_hours: float
    saved_gb_hours: float
    """GB-hour fields are rounded to 6 decimal places."""
    price_millicents_per_gb_hour: int
    always_on_millicents: int
    actual_millicents: int
    saved_millicents: int
    parked_ratio: float
    """saved_mb_seconds / always_on_mb_seconds."""
    methodology: str
    source: AppSavingsResponseSource
    as_of: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        slug = self.slug

        period_start = self.period_start.isoformat()

        period_end = self.period_end.isoformat()

        baseline_start = self.baseline_start.isoformat()

        baseline_instances = self.baseline_instances

        billable_ram_mb = self.billable_ram_mb

        always_on_mb_seconds = self.always_on_mb_seconds

        actual_mb_seconds = self.actual_mb_seconds

        saved_mb_seconds = self.saved_mb_seconds

        always_on_gb_hours = self.always_on_gb_hours

        actual_gb_hours = self.actual_gb_hours

        saved_gb_hours = self.saved_gb_hours

        price_millicents_per_gb_hour = self.price_millicents_per_gb_hour

        always_on_millicents = self.always_on_millicents

        actual_millicents = self.actual_millicents

        saved_millicents = self.saved_millicents

        parked_ratio = self.parked_ratio

        methodology = self.methodology

        source: str = self.source

        as_of = self.as_of.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "slug": slug,
                "period_start": period_start,
                "period_end": period_end,
                "baseline_start": baseline_start,
                "baseline_instances": baseline_instances,
                "billable_ram_mb": billable_ram_mb,
                "always_on_mb_seconds": always_on_mb_seconds,
                "actual_mb_seconds": actual_mb_seconds,
                "saved_mb_seconds": saved_mb_seconds,
                "always_on_gb_hours": always_on_gb_hours,
                "actual_gb_hours": actual_gb_hours,
                "saved_gb_hours": saved_gb_hours,
                "price_millicents_per_gb_hour": price_millicents_per_gb_hour,
                "always_on_millicents": always_on_millicents,
                "actual_millicents": actual_millicents,
                "saved_millicents": saved_millicents,
                "parked_ratio": parked_ratio,
                "methodology": methodology,
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

        baseline_start = datetime.datetime.fromisoformat(d.pop("baseline_start"))

        baseline_instances = d.pop("baseline_instances")

        billable_ram_mb = d.pop("billable_ram_mb")

        always_on_mb_seconds = d.pop("always_on_mb_seconds")

        actual_mb_seconds = d.pop("actual_mb_seconds")

        saved_mb_seconds = d.pop("saved_mb_seconds")

        always_on_gb_hours = d.pop("always_on_gb_hours")

        actual_gb_hours = d.pop("actual_gb_hours")

        saved_gb_hours = d.pop("saved_gb_hours")

        price_millicents_per_gb_hour = d.pop("price_millicents_per_gb_hour")

        always_on_millicents = d.pop("always_on_millicents")

        actual_millicents = d.pop("actual_millicents")

        saved_millicents = d.pop("saved_millicents")

        parked_ratio = d.pop("parked_ratio")

        methodology = d.pop("methodology")

        source = check_app_savings_response_source(d.pop("source"))

        as_of = datetime.datetime.fromisoformat(d.pop("as_of"))

        app_savings_response = cls(
            slug=slug,
            period_start=period_start,
            period_end=period_end,
            baseline_start=baseline_start,
            baseline_instances=baseline_instances,
            billable_ram_mb=billable_ram_mb,
            always_on_mb_seconds=always_on_mb_seconds,
            actual_mb_seconds=actual_mb_seconds,
            saved_mb_seconds=saved_mb_seconds,
            always_on_gb_hours=always_on_gb_hours,
            actual_gb_hours=actual_gb_hours,
            saved_gb_hours=saved_gb_hours,
            price_millicents_per_gb_hour=price_millicents_per_gb_hour,
            always_on_millicents=always_on_millicents,
            actual_millicents=actual_millicents,
            saved_millicents=saved_millicents,
            parked_ratio=parked_ratio,
            methodology=methodology,
            source=source,
            as_of=as_of,
        )

        app_savings_response.additional_properties = d
        return app_savings_response

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
