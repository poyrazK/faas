from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.managed_realtime_push_delivery_priority import (
    ManagedRealtimePushDeliveryPriority,
    check_managed_realtime_push_delivery_priority,
)
from ..models.managed_realtime_push_delivery_provider import (
    ManagedRealtimePushDeliveryProvider,
    check_managed_realtime_push_delivery_provider,
)
from ..models.managed_realtime_push_delivery_status import (
    ManagedRealtimePushDeliveryStatus,
    check_managed_realtime_push_delivery_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedRealtimePushDelivery")


@_attrs_define
class ManagedRealtimePushDelivery:
    """Push delivery attempt metadata without provider credentials or device tokens."""

    priority: ManagedRealtimePushDeliveryPriority
    category: str
    expires_at: datetime.datetime
    id: UUID
    device: str
    provider: ManagedRealtimePushDeliveryProvider
    message_id: str
    sequence: int
    status: ManagedRealtimePushDeliveryStatus
    attempts: int
    status_code: int
    """Provider HTTP status; zero means no response."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    next_attempt: datetime.datetime
    not_before: datetime.datetime | Unset = UNSET
    """Earliest scheduled delivery; epoch or zero time means no schedule."""
    collapse_key: str | Unset = UNSET
    group_key: str | Unset = UNSET
    group_label: str | Unset = UNSET
    digest_id: UUID | Unset = UNSET
    digest_count: int | Unset = UNSET
    """Distinct message count at the most recent prepared digest attempt."""
    code: str | Unset = UNSET
    """Sanitized outcome code; provider bodies are omitted."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        priority: str = self.priority

        category = self.category

        expires_at = self.expires_at.isoformat()

        id = str(self.id)

        device = self.device

        provider: str = self.provider

        message_id = self.message_id

        sequence = self.sequence

        status: str = self.status

        attempts = self.attempts

        status_code = self.status_code

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        next_attempt = self.next_attempt.isoformat()

        not_before: str | Unset = UNSET
        if not isinstance(self.not_before, Unset):
            not_before = self.not_before.isoformat()

        collapse_key = self.collapse_key

        group_key = self.group_key

        group_label = self.group_label

        digest_id: str | Unset = UNSET
        if not isinstance(self.digest_id, Unset):
            digest_id = str(self.digest_id)

        digest_count = self.digest_count

        code = self.code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "priority": priority,
                "category": category,
                "expires_at": expires_at,
                "id": id,
                "device": device,
                "provider": provider,
                "message_id": message_id,
                "sequence": sequence,
                "status": status,
                "attempts": attempts,
                "status_code": status_code,
                "created_at": created_at,
                "updated_at": updated_at,
                "next_attempt": next_attempt,
            }
        )
        if not_before is not UNSET:
            field_dict["not_before"] = not_before
        if collapse_key is not UNSET:
            field_dict["collapse_key"] = collapse_key
        if group_key is not UNSET:
            field_dict["group_key"] = group_key
        if group_label is not UNSET:
            field_dict["group_label"] = group_label
        if digest_id is not UNSET:
            field_dict["digest_id"] = digest_id
        if digest_count is not UNSET:
            field_dict["digest_count"] = digest_count
        if code is not UNSET:
            field_dict["code"] = code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        priority = check_managed_realtime_push_delivery_priority(d.pop("priority"))

        category = d.pop("category")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        id = UUID(d.pop("id"))

        device = d.pop("device")

        provider = check_managed_realtime_push_delivery_provider(d.pop("provider"))

        message_id = d.pop("message_id")

        sequence = d.pop("sequence")

        status = check_managed_realtime_push_delivery_status(d.pop("status"))

        attempts = d.pop("attempts")

        status_code = d.pop("status_code")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        next_attempt = datetime.datetime.fromisoformat(d.pop("next_attempt"))

        _not_before = d.pop("not_before", UNSET)
        not_before: datetime.datetime | Unset
        if isinstance(_not_before, Unset):
            not_before = UNSET
        else:
            not_before = datetime.datetime.fromisoformat(_not_before)

        collapse_key = d.pop("collapse_key", UNSET)

        group_key = d.pop("group_key", UNSET)

        group_label = d.pop("group_label", UNSET)

        _digest_id = d.pop("digest_id", UNSET)
        digest_id: UUID | Unset
        if isinstance(_digest_id, Unset):
            digest_id = UNSET
        else:
            digest_id = UUID(_digest_id)

        digest_count = d.pop("digest_count", UNSET)

        code = d.pop("code", UNSET)

        managed_realtime_push_delivery = cls(
            priority=priority,
            category=category,
            expires_at=expires_at,
            id=id,
            device=device,
            provider=provider,
            message_id=message_id,
            sequence=sequence,
            status=status,
            attempts=attempts,
            status_code=status_code,
            created_at=created_at,
            updated_at=updated_at,
            next_attempt=next_attempt,
            not_before=not_before,
            collapse_key=collapse_key,
            group_key=group_key,
            group_label=group_label,
            digest_id=digest_id,
            digest_count=digest_count,
            code=code,
        )

        managed_realtime_push_delivery.additional_properties = d
        return managed_realtime_push_delivery

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
