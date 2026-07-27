from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.delayed_task_request_payload import DelayedTaskRequestPayload


T = TypeVar("T", bound="DelayedTaskRequest")


@_attrs_define
class DelayedTaskRequest:
    """Body for POST /v1/apps/{slug}/delayed-tasks. scheduled_at must be in the future (UTC)."""

    scheduled_at: datetime.datetime
    payload: DelayedTaskRequestPayload | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        scheduled_at = self.scheduled_at.isoformat()

        payload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.payload, Unset):
            payload = self.payload.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "scheduled_at": scheduled_at,
            }
        )
        if payload is not UNSET:
            field_dict["payload"] = payload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.delayed_task_request_payload import DelayedTaskRequestPayload

        d = dict(src_dict)
        scheduled_at = datetime.datetime.fromisoformat(d.pop("scheduled_at"))

        _payload = d.pop("payload", UNSET)
        payload: DelayedTaskRequestPayload | Unset
        if isinstance(_payload, Unset):
            payload = UNSET
        else:
            payload = DelayedTaskRequestPayload.from_dict(_payload)

        delayed_task_request = cls(
            scheduled_at=scheduled_at,
            payload=payload,
        )

        delayed_task_request.additional_properties = d
        return delayed_task_request

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
