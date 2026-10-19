from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.outbound_ai_route_stats_strategy import (
    OutboundAIRouteStatsStrategy,
    check_outbound_ai_route_stats_strategy,
)

if TYPE_CHECKING:
    from ..models.outbound_ai_route_target_stats import OutboundAIRouteTargetStats


T = TypeVar("T", bound="OutboundAIRouteStats")


@_attrs_define
class OutboundAIRouteStats:
    """Each target's settled attempts since window_start, the window latency and cost routes judge (ADR-1434)."""

    strategy: OutboundAIRouteStatsStrategy
    window_start: datetime.datetime
    targets: list[OutboundAIRouteTargetStats]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        strategy: str = self.strategy

        window_start = self.window_start.isoformat()

        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "strategy": strategy,
                "window_start": window_start,
                "targets": targets,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_target_stats import OutboundAIRouteTargetStats

        d = dict(src_dict)
        strategy = check_outbound_ai_route_stats_strategy(d.pop("strategy"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIRouteTargetStats.from_dict(targets_item_data)

            targets.append(targets_item)

        outbound_ai_route_stats = cls(
            strategy=strategy,
            window_start=window_start,
            targets=targets,
        )

        outbound_ai_route_stats.additional_properties = d
        return outbound_ai_route_stats

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
