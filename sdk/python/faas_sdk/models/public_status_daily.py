from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.public_status_daily_status import PublicStatusDailyStatus, check_public_status_daily_status

T = TypeVar("T", bound="PublicStatusDaily")


@_attrs_define
class PublicStatusDaily:
    """One UTC day's status, uptime, and telemetry coverage observation."""

    date: datetime.date
    status: PublicStatusDailyStatus
    uptime_pct: float | None
    coverage_pct: float

    def to_dict(self) -> dict[str, Any]:
        date = self.date.isoformat()

        status: str = self.status

        uptime_pct: float | None
        uptime_pct = self.uptime_pct

        coverage_pct = self.coverage_pct

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "date": date,
                "status": status,
                "uptime_pct": uptime_pct,
                "coverage_pct": coverage_pct,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        date = datetime.date.fromisoformat(d.pop("date"))

        status = check_public_status_daily_status(d.pop("status"))

        def _parse_uptime_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        uptime_pct = _parse_uptime_pct(d.pop("uptime_pct"))

        coverage_pct = d.pop("coverage_pct")

        public_status_daily = cls(
            date=date,
            status=status,
            uptime_pct=uptime_pct,
            coverage_pct=coverage_pct,
        )

        return public_status_daily
