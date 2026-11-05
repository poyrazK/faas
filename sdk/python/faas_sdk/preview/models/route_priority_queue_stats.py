from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_priority_queue_stats_source import (
    RoutePriorityQueueStatsSource,
    check_route_priority_queue_stats_source,
)

if TYPE_CHECKING:
    from ..models.route_priority_queue_class import RoutePriorityQueueClass


T = TypeVar("T", bound="RoutePriorityQueueStats")


@_attrs_define
class RoutePriorityQueueStats:
    """How the app's warm-capacity queue behaved per priority class over the evidence window. Present on GET; source is
    unavailable when gateway metrics cannot be read.

    """

    source: RoutePriorityQueueStatsSource
    window: str
    """Window of the counts, for example 24h."""
    classes: list[RoutePriorityQueueClass]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        window = self.window

        classes = []
        for classes_item_data in self.classes:
            classes_item = classes_item_data.to_dict()
            classes.append(classes_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "window": window,
                "classes": classes,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_priority_queue_class import RoutePriorityQueueClass

        d = dict(src_dict)
        source = check_route_priority_queue_stats_source(d.pop("source"))

        window = d.pop("window")

        classes = []
        _classes = d.pop("classes")
        for classes_item_data in _classes:
            classes_item = RoutePriorityQueueClass.from_dict(classes_item_data)

            classes.append(classes_item)

        route_priority_queue_stats = cls(
            source=source,
            window=window,
            classes=classes,
        )

        route_priority_queue_stats.additional_properties = d
        return route_priority_queue_stats

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
