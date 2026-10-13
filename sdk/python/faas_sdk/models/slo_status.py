from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="SLOStatus")


@_attrs_define
class SLOStatus:
    """Error-budget position of one SLO, returned by GET /v1/apps/{slug}/slos/{id} (ADR-747)."""

    window_start: datetime.datetime
    """Start of the rolling window: the later of window_days ago and the hour the SLO was created."""
    hours_recorded: int
    """Completed hours meterd has recorded in the window."""
    hours_expected: int
    """Completed hours in the window; fewer recorded hours means history is still being backfilled or was lost."""
    good: int
    """Requests that met the SLI in the recorded hours."""
    total: int
    """Requests the SLI counted in the recorded hours."""
    attainment_pct: float | None
    """good / total as a percentage; null when the window had no requests."""
    budget_remaining_pct: float | None
    """Share of the error budget left: 100 when nothing failed, 0 when it is spent, negative once the objective is
    missed."""
    burn_rate_1h: float | None
    """Budget burn over the last hour, live from Prometheus: 1 spends exactly the budget over the window."""
    burn_rate_6h: float | None
    """Budget burn over the last six hours, live from Prometheus."""
    source: str
    """prometheus, or degraded: <reason> when budget history or burn rates are unavailable."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        window_start = self.window_start.isoformat()

        hours_recorded = self.hours_recorded

        hours_expected = self.hours_expected

        good = self.good

        total = self.total

        attainment_pct: float | None
        attainment_pct = self.attainment_pct

        budget_remaining_pct: float | None
        budget_remaining_pct = self.budget_remaining_pct

        burn_rate_1h: float | None
        burn_rate_1h = self.burn_rate_1h

        burn_rate_6h: float | None
        burn_rate_6h = self.burn_rate_6h

        source = self.source

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "window_start": window_start,
                "hours_recorded": hours_recorded,
                "hours_expected": hours_expected,
                "good": good,
                "total": total,
                "attainment_pct": attainment_pct,
                "budget_remaining_pct": budget_remaining_pct,
                "burn_rate_1h": burn_rate_1h,
                "burn_rate_6h": burn_rate_6h,
                "source": source,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        hours_recorded = d.pop("hours_recorded")

        hours_expected = d.pop("hours_expected")

        good = d.pop("good")

        total = d.pop("total")

        def _parse_attainment_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        attainment_pct = _parse_attainment_pct(d.pop("attainment_pct"))

        def _parse_budget_remaining_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        budget_remaining_pct = _parse_budget_remaining_pct(d.pop("budget_remaining_pct"))

        def _parse_burn_rate_1h(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        burn_rate_1h = _parse_burn_rate_1h(d.pop("burn_rate_1h"))

        def _parse_burn_rate_6h(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        burn_rate_6h = _parse_burn_rate_6h(d.pop("burn_rate_6h"))

        source = d.pop("source")

        slo_status = cls(
            window_start=window_start,
            hours_recorded=hours_recorded,
            hours_expected=hours_expected,
            good=good,
            total=total,
            attainment_pct=attainment_pct,
            budget_remaining_pct=budget_remaining_pct,
            burn_rate_1h=burn_rate_1h,
            burn_rate_6h=burn_rate_6h,
            source=source,
        )

        slo_status.additional_properties = d
        return slo_status

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
