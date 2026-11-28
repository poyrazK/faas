from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.realtime_worker_credential_request_permissions_item import (
    RealtimeWorkerCredentialRequestPermissionsItem,
    check_realtime_worker_credential_request_permissions_item,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="RealtimeWorkerCredentialRequest")


@_attrs_define
class RealtimeWorkerCredentialRequest:
    """Realtime Worker Credential Request."""

    name: str
    permissions: list[RealtimeWorkerCredentialRequestPermissionsItem]
    ttl_seconds: int | Unset = 86400
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        permissions = []
        for permissions_item_data in self.permissions:
            permissions_item: str = permissions_item_data
            permissions.append(permissions_item)

        ttl_seconds = self.ttl_seconds

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "permissions": permissions,
            }
        )
        if ttl_seconds is not UNSET:
            field_dict["ttl_seconds"] = ttl_seconds

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        permissions = []
        _permissions = d.pop("permissions")
        for permissions_item_data in _permissions:
            permissions_item = check_realtime_worker_credential_request_permissions_item(permissions_item_data)

            permissions.append(permissions_item)

        ttl_seconds = d.pop("ttl_seconds", UNSET)

        realtime_worker_credential_request = cls(
            name=name,
            permissions=permissions,
            ttl_seconds=ttl_seconds,
        )

        realtime_worker_credential_request.additional_properties = d
        return realtime_worker_credential_request

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
