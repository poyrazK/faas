from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.put_outbound_ai_route_targets_request_strategy import (
    PutOutboundAIRouteTargetsRequestStrategy,
    check_put_outbound_ai_route_targets_request_strategy,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.outbound_ai_route_target import OutboundAIRouteTarget


T = TypeVar("T", bound="PutOutboundAIRouteTargetsRequest")


@_attrs_define
class PutOutboundAIRouteTargetsRequest:
    """The complete ordered target list that replaces an AI route's targets, with its strategy (default ordered). New
    targets end the route's canary and reset its targets' recent attempts.

    """

    targets: list[OutboundAIRouteTarget]
    strategy: PutOutboundAIRouteTargetsRequestStrategy | Unset = UNSET
    """The route's strategy from now on, as on creation (ADR-1434); omitted means ordered."""
    max_ttfb_ms: int | Unset = UNSET
    """The cost route's latency target from now on, as on creation; omitted means none."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        targets = []
        for targets_item_data in self.targets:
            targets_item = targets_item_data.to_dict()
            targets.append(targets_item)

        strategy: str | Unset = UNSET
        if not isinstance(self.strategy, Unset):
            strategy = self.strategy

        max_ttfb_ms = self.max_ttfb_ms

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "targets": targets,
            }
        )
        if strategy is not UNSET:
            field_dict["strategy"] = strategy
        if max_ttfb_ms is not UNSET:
            field_dict["max_ttfb_ms"] = max_ttfb_ms

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.outbound_ai_route_target import OutboundAIRouteTarget

        d = dict(src_dict)
        targets = []
        _targets = d.pop("targets")
        for targets_item_data in _targets:
            targets_item = OutboundAIRouteTarget.from_dict(targets_item_data)

            targets.append(targets_item)

        _strategy = d.pop("strategy", UNSET)
        strategy: PutOutboundAIRouteTargetsRequestStrategy | Unset
        if isinstance(_strategy, Unset):
            strategy = UNSET
        else:
            strategy = check_put_outbound_ai_route_targets_request_strategy(_strategy)

        max_ttfb_ms = d.pop("max_ttfb_ms", UNSET)

        put_outbound_ai_route_targets_request = cls(
            targets=targets,
            strategy=strategy,
            max_ttfb_ms=max_ttfb_ms,
        )

        put_outbound_ai_route_targets_request.additional_properties = d
        return put_outbound_ai_route_targets_request

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
