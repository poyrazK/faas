from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="CreatePrivateNetworkPeeringRequest")


@_attrs_define
class CreatePrivateNetworkPeeringRequest:
    """POST /v1/networks/{id}/peerings body."""

    peer_network_id: str
    """Another Gregale-owned network in the same account and region."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        peer_network_id = self.peer_network_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "peer_network_id": peer_network_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        peer_network_id = d.pop("peer_network_id")

        create_private_network_peering_request = cls(
            peer_network_id=peer_network_id,
        )

        create_private_network_peering_request.additional_properties = d
        return create_private_network_peering_request

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
