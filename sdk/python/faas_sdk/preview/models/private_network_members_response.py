from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.private_network_member import PrivateNetworkMember


T = TypeVar("T", bound="PrivateNetworkMembersResponse")


@_attrs_define
class PrivateNetworkMembersResponse:
    """Account-scoped member inventory and address capacity for one network."""

    network_id: str
    cidr: str
    capacity: int
    """Allocatable member addresses after network, gateway, and broadcast reservations."""
    used: int
    available: int
    members: list[PrivateNetworkMember]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        network_id = self.network_id

        cidr = self.cidr

        capacity = self.capacity

        used = self.used

        available = self.available

        members = []
        for members_item_data in self.members:
            members_item = members_item_data.to_dict()
            members.append(members_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "network_id": network_id,
                "cidr": cidr,
                "capacity": capacity,
                "used": used,
                "available": available,
                "members": members,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.private_network_member import PrivateNetworkMember

        d = dict(src_dict)
        network_id = d.pop("network_id")

        cidr = d.pop("cidr")

        capacity = d.pop("capacity")

        used = d.pop("used")

        available = d.pop("available")

        members = []
        _members = d.pop("members")
        for members_item_data in _members:
            members_item = PrivateNetworkMember.from_dict(members_item_data)

            members.append(members_item)

        private_network_members_response = cls(
            network_id=network_id,
            cidr=cidr,
            capacity=capacity,
            used=used,
            available=available,
            members=members,
        )

        private_network_members_response.additional_properties = d
        return private_network_members_response

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
