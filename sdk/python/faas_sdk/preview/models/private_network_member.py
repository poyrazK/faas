from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PrivateNetworkMember")


@_attrs_define
class PrivateNetworkMember:
    """Stable address reservation for one member of a Gregale-owned network."""

    id: UUID
    owner_type: str
    """Platform resource kind that owns the reservation, such as app."""
    owner_id: str
    """Opaque platform resource identifier."""
    address: str
    created_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        owner_type = self.owner_type

        owner_id = self.owner_id

        address = self.address

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "owner_type": owner_type,
                "owner_id": owner_id,
                "address": address,
            }
        )
        if created_at is not UNSET:
            field_dict["created_at"] = created_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        owner_type = d.pop("owner_type")

        owner_id = d.pop("owner_id")

        address = d.pop("address")

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        private_network_member = cls(
            id=id,
            owner_type=owner_type,
            owner_id=owner_id,
            address=address,
            created_at=created_at,
        )

        private_network_member.additional_properties = d
        return private_network_member

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
