from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.private_network_status import PrivateNetworkStatus, check_private_network_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="PrivateNetwork")


@_attrs_define
class PrivateNetwork:
    """Gregale-owned private-network definition."""

    id: str
    name: str
    region: str
    cidr: str
    """Canonical IPv4 RFC1918 range, /16 through /28."""
    status: PrivateNetworkStatus
    allowed_cidrs: list[str] | Unset = UNSET
    """Optional reusable CIDR allowlist for all attached workloads."""
    status_detail: str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        name = self.name

        region = self.region

        cidr = self.cidr

        status: str = self.status

        allowed_cidrs: list[str] | Unset = UNSET
        if not isinstance(self.allowed_cidrs, Unset):
            allowed_cidrs = self.allowed_cidrs

        status_detail = self.status_detail

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "name": name,
                "region": region,
                "cidr": cidr,
                "status": status,
            }
        )
        if allowed_cidrs is not UNSET:
            field_dict["allowed_cidrs"] = allowed_cidrs
        if status_detail is not UNSET:
            field_dict["status_detail"] = status_detail
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = d.pop("id")

        name = d.pop("name")

        region = d.pop("region")

        cidr = d.pop("cidr")

        status = check_private_network_status(d.pop("status"))

        allowed_cidrs = cast(list[str], d.pop("allowed_cidrs", UNSET))

        status_detail = d.pop("status_detail", UNSET)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        private_network = cls(
            id=id,
            name=name,
            region=region,
            cidr=cidr,
            status=status,
            allowed_cidrs=allowed_cidrs,
            status_detail=status_detail,
            created_at=created_at,
            updated_at=updated_at,
        )

        private_network.additional_properties = d
        return private_network

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
