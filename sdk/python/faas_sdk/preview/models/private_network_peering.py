from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.private_network_peering_status import PrivateNetworkPeeringStatus, check_private_network_peering_status
from ..types import UNSET, Unset

T = TypeVar("T", bound="PrivateNetworkPeering")


@_attrs_define
class PrivateNetworkPeering:
    """Durable, provider-neutral peering intent between two Gregale-owned networks."""

    id: str
    network_id: str
    peer_network_id: str
    region: str
    status: PrivateNetworkPeeringStatus
    status_detail: str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = self.id

        network_id = self.network_id

        peer_network_id = self.peer_network_id

        region = self.region

        status: str = self.status

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
                "network_id": network_id,
                "peer_network_id": peer_network_id,
                "region": region,
                "status": status,
            }
        )
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

        network_id = d.pop("network_id")

        peer_network_id = d.pop("peer_network_id")

        region = d.pop("region")

        status = check_private_network_peering_status(d.pop("status"))

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

        private_network_peering = cls(
            id=id,
            network_id=network_id,
            peer_network_id=peer_network_id,
            region=region,
            status=status,
            status_detail=status_detail,
            created_at=created_at,
            updated_at=updated_at,
        )

        private_network_peering.additional_properties = d
        return private_network_peering

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
