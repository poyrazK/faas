from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresAutoGrowStatus")


@_attrs_define
class ManagedPostgresAutoGrowStatus:
    """Automatic growth policy and the latest storage eligibility check."""

    max_bytes: int
    reason: str
    """Current policy or resize outcome code."""
    proposed_bytes: int | Unset = UNSET
    resize_id: UUID | Unset = UNSET
    last_grown_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        max_bytes = self.max_bytes

        reason = self.reason

        proposed_bytes = self.proposed_bytes

        resize_id: str | Unset = UNSET
        if not isinstance(self.resize_id, Unset):
            resize_id = str(self.resize_id)

        last_grown_at: str | Unset = UNSET
        if not isinstance(self.last_grown_at, Unset):
            last_grown_at = self.last_grown_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "max_bytes": max_bytes,
                "reason": reason,
            }
        )
        if proposed_bytes is not UNSET:
            field_dict["proposed_bytes"] = proposed_bytes
        if resize_id is not UNSET:
            field_dict["resize_id"] = resize_id
        if last_grown_at is not UNSET:
            field_dict["last_grown_at"] = last_grown_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        max_bytes = d.pop("max_bytes")

        reason = d.pop("reason")

        proposed_bytes = d.pop("proposed_bytes", UNSET)

        _resize_id = d.pop("resize_id", UNSET)
        resize_id: UUID | Unset
        if isinstance(_resize_id, Unset):
            resize_id = UNSET
        else:
            resize_id = UUID(_resize_id)

        _last_grown_at = d.pop("last_grown_at", UNSET)
        last_grown_at: datetime.datetime | Unset
        if isinstance(_last_grown_at, Unset):
            last_grown_at = UNSET
        else:
            last_grown_at = datetime.datetime.fromisoformat(_last_grown_at)

        managed_postgres_auto_grow_status = cls(
            max_bytes=max_bytes,
            reason=reason,
            proposed_bytes=proposed_bytes,
            resize_id=resize_id,
            last_grown_at=last_grown_at,
        )

        managed_postgres_auto_grow_status.additional_properties = d
        return managed_postgres_auto_grow_status

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
