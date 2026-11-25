from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.managed_postgres_session import ManagedPostgresSession


T = TypeVar("T", bound="ManagedPostgresSessionSample")


@_attrs_define
class ManagedPostgresSessionSample:
    sampled_at: datetime.datetime
    has_more: bool
    sessions: list[ManagedPostgresSession]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sampled_at = self.sampled_at.isoformat()

        has_more = self.has_more

        sessions = []
        for sessions_item_data in self.sessions:
            sessions_item = sessions_item_data.to_dict()
            sessions.append(sessions_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sampled_at": sampled_at,
                "has_more": has_more,
                "sessions": sessions,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.managed_postgres_session import ManagedPostgresSession

        d = dict(src_dict)
        sampled_at = datetime.datetime.fromisoformat(d.pop("sampled_at"))

        has_more = d.pop("has_more")

        sessions = []
        _sessions = d.pop("sessions")
        for sessions_item_data in _sessions:
            sessions_item = ManagedPostgresSession.from_dict(sessions_item_data)

            sessions.append(sessions_item)

        managed_postgres_session_sample = cls(
            sampled_at=sampled_at,
            has_more=has_more,
            sessions=sessions,
        )

        managed_postgres_session_sample.additional_properties = d
        return managed_postgres_session_sample

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
