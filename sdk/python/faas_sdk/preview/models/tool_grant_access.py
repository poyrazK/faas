from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolGrantAccess")


@_attrs_define
class ToolGrantAccess:
    """Currently active grant for this connection and exact described tool; omits other tools and owner-only grant
    metadata.

    """

    grant_id: UUID
    expires_at: datetime.datetime
    allow_invocation: bool
    credential_binding_sha256: str | Unset = UNSET
    """Opaque reference required for this private invocation grant"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        grant_id = str(self.grant_id)

        expires_at = self.expires_at.isoformat()

        allow_invocation = self.allow_invocation

        credential_binding_sha256 = self.credential_binding_sha256

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "grant_id": grant_id,
                "expires_at": expires_at,
                "allow_invocation": allow_invocation,
            }
        )
        if credential_binding_sha256 is not UNSET:
            field_dict["credential_binding_sha256"] = credential_binding_sha256

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        grant_id = UUID(d.pop("grant_id"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        allow_invocation = d.pop("allow_invocation")

        credential_binding_sha256 = d.pop("credential_binding_sha256", UNSET)

        tool_grant_access = cls(
            grant_id=grant_id,
            expires_at=expires_at,
            allow_invocation=allow_invocation,
            credential_binding_sha256=credential_binding_sha256,
        )

        tool_grant_access.additional_properties = d
        return tool_grant_access

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
