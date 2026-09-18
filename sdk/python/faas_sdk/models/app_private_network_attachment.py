from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_private_network_attachment_status import (
    AppPrivateNetworkAttachmentStatus,
    check_app_private_network_attachment_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="AppPrivateNetworkAttachment")


@_attrs_define
class AppPrivateNetworkAttachment:
    """Provider-neutral private-network attachment intent for one app.
    `pending` and `error` are fail-closed; only `ready` admits private
    network traffic after a connector has reconciled the request.

    """

    id: UUID
    network_id: str
    region: str
    cidrs: list[str]
    status: AppPrivateNetworkAttachmentStatus
    allowed_cidrs: list[str] | Unset = UNSET
    """Optional private-network policy. Empty preserves allow-all behavior; populated ranges are admitted
    symmetrically for private egress and ingress."""
    address: str | Unset = UNSET
    """Stable Gregale member address for this app when the fabric is enabled."""
    status_detail: str | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        network_id = self.network_id

        region = self.region

        cidrs = self.cidrs

        status: str = self.status

        allowed_cidrs: list[str] | Unset = UNSET
        if not isinstance(self.allowed_cidrs, Unset):
            allowed_cidrs = self.allowed_cidrs

        address = self.address

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
                "region": region,
                "cidrs": cidrs,
                "status": status,
            }
        )
        if allowed_cidrs is not UNSET:
            field_dict["allowed_cidrs"] = allowed_cidrs
        if address is not UNSET:
            field_dict["address"] = address
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
        id = UUID(d.pop("id"))

        network_id = d.pop("network_id")

        region = d.pop("region")

        cidrs = cast(list[str], d.pop("cidrs"))

        status = check_app_private_network_attachment_status(d.pop("status"))

        allowed_cidrs = cast(list[str], d.pop("allowed_cidrs", UNSET))

        address = d.pop("address", UNSET)

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

        app_private_network_attachment = cls(
            id=id,
            network_id=network_id,
            region=region,
            cidrs=cidrs,
            status=status,
            allowed_cidrs=allowed_cidrs,
            address=address,
            status_detail=status_detail,
            created_at=created_at,
            updated_at=updated_at,
        )

        app_private_network_attachment.additional_properties = d
        return app_private_network_attachment

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
