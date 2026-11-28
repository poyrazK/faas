from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.public_status_component_id import PublicStatusComponentId, check_public_status_component_id
from ..models.public_status_component_status import PublicStatusComponentStatus, check_public_status_component_status

if TYPE_CHECKING:
    from ..models.public_status_daily import PublicStatusDaily


T = TypeVar("T", bound="PublicStatusComponent")


@_attrs_define
class PublicStatusComponent:
    """Current and 30-day status summary for one public capability."""

    id: PublicStatusComponentId
    name: str
    status: PublicStatusComponentStatus
    uptime_30d_pct: float | None
    coverage_30d_pct: float
    daily: list[PublicStatusDaily]

    def to_dict(self) -> dict[str, Any]:
        id: str = self.id

        name = self.name

        status: str = self.status

        uptime_30d_pct: float | None
        uptime_30d_pct = self.uptime_30d_pct

        coverage_30d_pct = self.coverage_30d_pct

        daily = []
        for daily_item_data in self.daily:
            daily_item = daily_item_data.to_dict()
            daily.append(daily_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "name": name,
                "status": status,
                "uptime_30d_pct": uptime_30d_pct,
                "coverage_30d_pct": coverage_30d_pct,
                "daily": daily,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.public_status_daily import PublicStatusDaily

        d = dict(src_dict)
        id = check_public_status_component_id(d.pop("id"))

        name = d.pop("name")

        status = check_public_status_component_status(d.pop("status"))

        def _parse_uptime_30d_pct(data: object) -> float | None:
            if data is None:
                return data
            return cast(float | None, data)

        uptime_30d_pct = _parse_uptime_30d_pct(d.pop("uptime_30d_pct"))

        coverage_30d_pct = d.pop("coverage_30d_pct")

        daily = []
        _daily = d.pop("daily")
        for daily_item_data in _daily:
            daily_item = PublicStatusDaily.from_dict(daily_item_data)

            daily.append(daily_item)

        public_status_component = cls(
            id=id,
            name=name,
            status=status,
            uptime_30d_pct=uptime_30d_pct,
            coverage_30d_pct=coverage_30d_pct,
            daily=daily,
        )

        return public_status_component
