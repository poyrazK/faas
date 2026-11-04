from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_server_key_slot import DataAPIServerKeySlot, check_data_api_server_key_slot
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_api_server_key_permission_set import DataAPIServerKeyPermissionSet


T = TypeVar("T", bound="DataAPIServerKey")


@_attrs_define
class DataAPIServerKey:
    """One server-key rotation slot as the runtime reports it, the SHA-256 of the key and any permissions."""

    slot: DataAPIServerKeySlot
    sha256: str
    permissions: DataAPIServerKeyPermissionSet | Unset = UNSET
    """Narrows a server key. Absent fields keep full access; live reads use relations and subjects."""

    def to_dict(self) -> dict[str, Any]:
        slot: str = self.slot

        sha256 = self.sha256

        permissions: dict[str, Any] | Unset = UNSET
        if not isinstance(self.permissions, Unset):
            permissions = self.permissions.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "slot": slot,
                "sha256": sha256,
            }
        )
        if permissions is not UNSET:
            field_dict["permissions"] = permissions

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_server_key_permission_set import DataAPIServerKeyPermissionSet

        d = dict(src_dict)
        slot = check_data_api_server_key_slot(d.pop("slot"))

        sha256 = d.pop("sha256")

        _permissions = d.pop("permissions", UNSET)
        permissions: DataAPIServerKeyPermissionSet | Unset
        if isinstance(_permissions, Unset):
            permissions = UNSET
        else:
            permissions = DataAPIServerKeyPermissionSet.from_dict(_permissions)

        data_api_server_key = cls(
            slot=slot,
            sha256=sha256,
            permissions=permissions,
        )

        return data_api_server_key
