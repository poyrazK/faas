from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.route_priority_rule import RoutePriorityRule


T = TypeVar("T", bound="RouteAdviceSetting")


@_attrs_define
class RouteAdviceSetting:
    """The app setting a wake_ahead or priority suggestion proposes; apply it through PUT /v1/apps/{slug}/service-wake-
    ahead or PUT /v1/apps/{slug}/route-priorities.

    """

    wake_ahead: bool | Unset = UNSET
    """Turn service wake-ahead on."""
    route_priorities: list[RoutePriorityRule] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        wake_ahead = self.wake_ahead

        route_priorities: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.route_priorities, Unset):
            route_priorities = []
            for route_priorities_item_data in self.route_priorities:
                route_priorities_item = route_priorities_item_data.to_dict()
                route_priorities.append(route_priorities_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if wake_ahead is not UNSET:
            field_dict["wake_ahead"] = wake_ahead
        if route_priorities is not UNSET:
            field_dict["route_priorities"] = route_priorities

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_priority_rule import RoutePriorityRule

        d = dict(src_dict)
        wake_ahead = d.pop("wake_ahead", UNSET)

        _route_priorities = d.pop("route_priorities", UNSET)
        route_priorities: list[RoutePriorityRule] | Unset = UNSET
        if _route_priorities is not UNSET:
            route_priorities = []
            for route_priorities_item_data in _route_priorities:
                route_priorities_item = RoutePriorityRule.from_dict(route_priorities_item_data)

                route_priorities.append(route_priorities_item)

        route_advice_setting = cls(
            wake_ahead=wake_ahead,
            route_priorities=route_priorities,
        )

        route_advice_setting.additional_properties = d
        return route_advice_setting

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
