from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.delayed_task_response_state import DelayedTaskResponseState, check_delayed_task_response_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="DelayedTaskResponse")


@_attrs_define
class DelayedTaskResponse:
    """Delayed task create/get shape. ScheduledAt is the customer-facing UTC dispatch time. State is populated on get,
    omitted on create (always `pending` there).

    """

    id: str
    scheduled_at: datetime.datetime
    state: DelayedTaskResponseState | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        scheduled_at = self.scheduled_at.isoformat()

        state: str | Unset = UNSET
        if not isinstance(self.state, Unset):
            state = self.state

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "scheduled_at": scheduled_at,
            }
        )
        if state is not UNSET:
            field_dict["state"] = state

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        scheduled_at = datetime.datetime.fromisoformat(d.pop("scheduled_at"))

        _state = d.pop("state", UNSET)
        state: DelayedTaskResponseState | Unset
        if isinstance(_state, Unset):
            state = UNSET
        else:
            state = check_delayed_task_response_state(_state)

        delayed_task_response = cls(
            id=id,
            scheduled_at=scheduled_at,
            state=state,
        )

        delayed_task_response.additional_properties = d
        return delayed_task_response

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
