from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.agent_activity_entry import AgentActivityEntry


T = TypeVar("T", bound="AgentActivityResponse")


@_attrs_define
class AgentActivityResponse:
    """One page of an agent's write requests, newest first."""

    activity: list[AgentActivityEntry]
    next_before: str | Unset = UNSET
    """Pass as `before` to read the next older page; absent on the last page."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        activity = []
        for activity_item_data in self.activity:
            activity_item = activity_item_data.to_dict()
            activity.append(activity_item)

        next_before = self.next_before

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "activity": activity,
            }
        )
        if next_before is not UNSET:
            field_dict["next_before"] = next_before

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.agent_activity_entry import AgentActivityEntry

        d = dict(src_dict)
        activity = []
        _activity = d.pop("activity")
        for activity_item_data in _activity:
            activity_item = AgentActivityEntry.from_dict(activity_item_data)

            activity.append(activity_item)

        next_before = d.pop("next_before", UNSET)

        agent_activity_response = cls(
            activity=activity,
            next_before=next_before,
        )

        agent_activity_response.additional_properties = d
        return agent_activity_response

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
