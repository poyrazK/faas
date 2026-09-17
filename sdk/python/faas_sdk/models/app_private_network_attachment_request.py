from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="AppPrivateNetworkAttachmentRequest")


@_attrs_define
class AppPrivateNetworkAttachmentRequest:
    """PUT body for /v1/apps/{slug}/network/private."""

    network_id: str
    region: str | Unset = UNSET
    """Optional when attaching a Gregale-owned network; it must match the network region."""
    cidrs: list[str] | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        network_id = self.network_id

        region = self.region

        cidrs: list[str] | Unset = UNSET
        if not isinstance(self.cidrs, Unset):
            cidrs = self.cidrs

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "network_id": network_id,
            }
        )
        if region is not UNSET:
            field_dict["region"] = region
        if cidrs is not UNSET:
            field_dict["cidrs"] = cidrs

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        network_id = d.pop("network_id")

        region = d.pop("region", UNSET)

        cidrs = cast(list[str], d.pop("cidrs", UNSET))

        app_private_network_attachment_request = cls(
            network_id=network_id,
            region=region,
            cidrs=cidrs,
        )

        app_private_network_attachment_request.additional_properties = d
        return app_private_network_attachment_request

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
