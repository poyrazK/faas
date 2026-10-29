from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_priority_queue_class_class import (
    RoutePriorityQueueClassClass,
    check_route_priority_queue_class_class,
)

T = TypeVar("T", bound="RoutePriorityQueueClass")


@_attrs_define
class RoutePriorityQueueClass:
    """Queue outcomes for one priority class."""

    class_: RoutePriorityQueueClassClass
    queued: int
    """Requests that waited for capacity."""
    displacing: int
    """Requests that took a lower class's queue place."""
    displaced: int
    """Requests that lost their place to a higher class."""
    rejected: int
    """Requests turned away because the queue was full."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        class_: str = self.class_

        queued = self.queued

        displacing = self.displacing

        displaced = self.displaced

        rejected = self.rejected

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "class": class_,
                "queued": queued,
                "displacing": displacing,
                "displaced": displaced,
                "rejected": rejected,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        class_ = check_route_priority_queue_class_class(d.pop("class"))

        queued = d.pop("queued")

        displacing = d.pop("displacing")

        displaced = d.pop("displaced")

        rejected = d.pop("rejected")

        route_priority_queue_class = cls(
            class_=class_,
            queued=queued,
            displacing=displacing,
            displaced=displaced,
            rejected=rejected,
        )

        route_priority_queue_class.additional_properties = d
        return route_priority_queue_class

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
