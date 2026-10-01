from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CommitBlockedEventResponse")


@_attrs_define
class CommitBlockedEventResponse:
    event_id: UUID
    type_: str
    blocked_code: str
    created_at: datetime.datetime
    observed_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        event_id = str(self.event_id)

        type_ = self.type_

        blocked_code = self.blocked_code

        created_at = self.created_at.isoformat()

        observed_at = self.observed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "event_id": event_id,
                "type": type_,
                "blocked_code": blocked_code,
                "created_at": created_at,
                "observed_at": observed_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        event_id = UUID(d.pop("event_id"))

        type_ = d.pop("type")

        blocked_code = d.pop("blocked_code")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        observed_at = datetime.datetime.fromisoformat(d.pop("observed_at"))

        commit_blocked_event_response = cls(
            event_id=event_id,
            type_=type_,
            blocked_code=blocked_code,
            created_at=created_at,
            observed_at=observed_at,
        )

        commit_blocked_event_response.additional_properties = d
        return commit_blocked_event_response

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
