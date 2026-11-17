from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_grant_credential_scope import ToolGrantCredentialScope, check_tool_grant_credential_scope
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolGrantCredential")


@_attrs_define
class ToolGrantCredential:
    """Owner-readable immutable credential binding; contains no plaintext token. Required scopes follow the existing 4-KiB
    review metadata bound.

    """

    sha256: str
    source_sha256: str
    """Static source ciphertext digest or managed connection authorization digest; source replacement or resealing
    invalidates the binding"""
    scope: ToolGrantCredentialScope
    required_scopes: list[str]
    connection_id: UUID | Unset = UNSET
    """Present only for a managed execution OAuth connection"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        sha256 = self.sha256

        source_sha256 = self.source_sha256

        scope: str = self.scope

        required_scopes = self.required_scopes

        connection_id: str | Unset = UNSET
        if not isinstance(self.connection_id, Unset):
            connection_id = str(self.connection_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "sha256": sha256,
                "source_sha256": source_sha256,
                "scope": scope,
                "required_scopes": required_scopes,
            }
        )
        if connection_id is not UNSET:
            field_dict["connection_id"] = connection_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        sha256 = d.pop("sha256")

        source_sha256 = d.pop("source_sha256")

        scope = check_tool_grant_credential_scope(d.pop("scope"))

        required_scopes = cast(list[str], d.pop("required_scopes"))

        _connection_id = d.pop("connection_id", UNSET)
        connection_id: UUID | Unset
        if isinstance(_connection_id, Unset):
            connection_id = UNSET
        else:
            connection_id = UUID(_connection_id)

        tool_grant_credential = cls(
            sha256=sha256,
            source_sha256=source_sha256,
            scope=scope,
            required_scopes=required_scopes,
            connection_id=connection_id,
        )

        tool_grant_credential.additional_properties = d
        return tool_grant_credential

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
