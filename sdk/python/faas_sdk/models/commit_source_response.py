from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="CommitSourceResponse")


@_attrs_define
class CommitSourceResponse:
    """Commit source destination, enabled state and latest bounded relay health observation."""

    id: UUID
    app_id: UUID
    name: str
    enabled: bool
    operation_policy: str | Unset = UNSET
    """Immutable managed Operations policy. Absent only for legacy internal sources."""
    relay_status: str | Unset = UNSET
    last_checked_at: datetime.datetime | Unset = UNSET
    pending_events: int | Unset = UNSET
    blocked_events: int | Unset = UNSET
    oldest_pending_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        app_id = str(self.app_id)

        name = self.name

        enabled = self.enabled

        operation_policy = self.operation_policy

        relay_status = self.relay_status

        last_checked_at: str | Unset = UNSET
        if not isinstance(self.last_checked_at, Unset):
            last_checked_at = self.last_checked_at.isoformat()

        pending_events = self.pending_events

        blocked_events = self.blocked_events

        oldest_pending_at: str | Unset = UNSET
        if not isinstance(self.oldest_pending_at, Unset):
            oldest_pending_at = self.oldest_pending_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "app_id": app_id,
                "name": name,
                "enabled": enabled,
            }
        )
        if operation_policy is not UNSET:
            field_dict["operation_policy"] = operation_policy
        if relay_status is not UNSET:
            field_dict["relay_status"] = relay_status
        if last_checked_at is not UNSET:
            field_dict["last_checked_at"] = last_checked_at
        if pending_events is not UNSET:
            field_dict["pending_events"] = pending_events
        if blocked_events is not UNSET:
            field_dict["blocked_events"] = blocked_events
        if oldest_pending_at is not UNSET:
            field_dict["oldest_pending_at"] = oldest_pending_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        app_id = UUID(d.pop("app_id"))

        name = d.pop("name")

        enabled = d.pop("enabled")

        operation_policy = d.pop("operation_policy", UNSET)

        relay_status = d.pop("relay_status", UNSET)

        _last_checked_at = d.pop("last_checked_at", UNSET)
        last_checked_at: datetime.datetime | Unset
        if isinstance(_last_checked_at, Unset):
            last_checked_at = UNSET
        else:
            last_checked_at = datetime.datetime.fromisoformat(_last_checked_at)

        pending_events = d.pop("pending_events", UNSET)

        blocked_events = d.pop("blocked_events", UNSET)

        _oldest_pending_at = d.pop("oldest_pending_at", UNSET)
        oldest_pending_at: datetime.datetime | Unset
        if isinstance(_oldest_pending_at, Unset):
            oldest_pending_at = UNSET
        else:
            oldest_pending_at = datetime.datetime.fromisoformat(_oldest_pending_at)

        commit_source_response = cls(
            id=id,
            app_id=app_id,
            name=name,
            enabled=enabled,
            operation_policy=operation_policy,
            relay_status=relay_status,
            last_checked_at=last_checked_at,
            pending_events=pending_events,
            blocked_events=blocked_events,
            oldest_pending_at=oldest_pending_at,
        )

        commit_source_response.additional_properties = d
        return commit_source_response

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
