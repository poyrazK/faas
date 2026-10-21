from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.data_api_server_key_permission_set_access import (
    DataAPIServerKeyPermissionSetAccess,
    check_data_api_server_key_permission_set_access,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataAPIServerKeyPermissionSet")


@_attrs_define
class DataAPIServerKeyPermissionSet:
    """Narrows a server key. Absent fields keep full access; live reads use relations and subjects."""

    access: DataAPIServerKeyPermissionSetAccess | Unset = UNSET
    relations: list[str] | Unset = UNSET
    rpcs: list[str] | Unset = UNSET
    subjects: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        access: str | Unset = UNSET
        if not isinstance(self.access, Unset):
            access = self.access

        relations: list[str] | Unset = UNSET
        if not isinstance(self.relations, Unset):
            relations = self.relations

        rpcs: list[str] | Unset = UNSET
        if not isinstance(self.rpcs, Unset):
            rpcs = self.rpcs

        subjects = self.subjects

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if access is not UNSET:
            field_dict["access"] = access
        if relations is not UNSET:
            field_dict["relations"] = relations
        if rpcs is not UNSET:
            field_dict["rpcs"] = rpcs
        if subjects is not UNSET:
            field_dict["subjects"] = subjects

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _access = d.pop("access", UNSET)
        access: DataAPIServerKeyPermissionSetAccess | Unset
        if isinstance(_access, Unset):
            access = UNSET
        else:
            access = check_data_api_server_key_permission_set_access(_access)

        relations = cast(list[str], d.pop("relations", UNSET))

        rpcs = cast(list[str], d.pop("rpcs", UNSET))

        subjects = d.pop("subjects", UNSET)

        data_api_server_key_permission_set = cls(
            access=access,
            relations=relations,
            rpcs=rpcs,
            subjects=subjects,
        )

        return data_api_server_key_permission_set
