from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ManagedPostgresLiveKey")


@_attrs_define
class ManagedPostgresLiveKey:
    """One live secret, identified by its key ID; key material is never returned."""

    key_id: str
    active: bool
    created_at: datetime.datetime
    retires_at: datetime.datetime | Unset = UNSET
    """When a rotated-out secret stops opening seals."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key_id = self.key_id

        active = self.active

        created_at = self.created_at.isoformat()

        retires_at: str | Unset = UNSET
        if not isinstance(self.retires_at, Unset):
            retires_at = self.retires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key_id": key_id,
                "active": active,
                "created_at": created_at,
            }
        )
        if retires_at is not UNSET:
            field_dict["retires_at"] = retires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key_id = d.pop("key_id")

        active = d.pop("active")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _retires_at = d.pop("retires_at", UNSET)
        retires_at: datetime.datetime | Unset
        if isinstance(_retires_at, Unset):
            retires_at = UNSET
        else:
            retires_at = datetime.datetime.fromisoformat(_retires_at)

        managed_postgres_live_key = cls(
            key_id=key_id,
            active=active,
            created_at=created_at,
            retires_at=retires_at,
        )

        managed_postgres_live_key.additional_properties = d
        return managed_postgres_live_key

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
