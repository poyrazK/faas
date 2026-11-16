from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolGrantIntent")


@_attrs_define
class ToolGrantIntent:
    """Exact immutable delegation to an existing agent key. Expiry uses microsecond precision; discovery, invocation
    consent and single-dispatch authority are checked separately.

    """

    key_id: str
    """Exact existing native agent connection key identity"""
    version: str
    name: str
    expires_at: datetime.datetime
    """Future expiry within 30 days and no later than the connection key expiry"""
    allow_invocation: bool = False
    """Owner permits calls through this exact grant; hosted connections also require independent invocation consent
    and operator enablement"""
    credential_secret_ref: str | Unset = UNSET
    """Optional existing secret:MCP_* access token in the exact app default environment; exclusive with
    credential_connection_id. Never a value."""
    credential_connection_id: UUID | Unset = UNSET
    """Optional exact owner-approved managed execution connection; private invocation grants only and exclusive
    with credential_secret_ref."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        key_id = self.key_id

        version = self.version

        name = self.name

        expires_at = self.expires_at.isoformat()

        allow_invocation = self.allow_invocation

        credential_secret_ref = self.credential_secret_ref

        credential_connection_id: str | Unset = UNSET
        if not isinstance(self.credential_connection_id, Unset):
            credential_connection_id = str(self.credential_connection_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "key_id": key_id,
                "version": version,
                "name": name,
                "expires_at": expires_at,
                "allow_invocation": allow_invocation,
            }
        )
        if credential_secret_ref is not UNSET:
            field_dict["credential_secret_ref"] = credential_secret_ref
        if credential_connection_id is not UNSET:
            field_dict["credential_connection_id"] = credential_connection_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        key_id = d.pop("key_id")

        version = d.pop("version")

        name = d.pop("name")

        expires_at = datetime.datetime.fromisoformat(d.pop("expires_at"))

        allow_invocation = d.pop("allow_invocation")

        credential_secret_ref = d.pop("credential_secret_ref", UNSET)

        _credential_connection_id = d.pop("credential_connection_id", UNSET)
        credential_connection_id: UUID | Unset
        if isinstance(_credential_connection_id, Unset):
            credential_connection_id = UNSET
        else:
            credential_connection_id = UUID(_credential_connection_id)

        tool_grant_intent = cls(
            key_id=key_id,
            version=version,
            name=name,
            expires_at=expires_at,
            allow_invocation=allow_invocation,
            credential_secret_ref=credential_secret_ref,
            credential_connection_id=credential_connection_id,
        )

        tool_grant_intent.additional_properties = d
        return tool_grant_intent

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
