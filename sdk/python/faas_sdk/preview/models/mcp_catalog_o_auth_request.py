from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.mcp_catalog_o_auth_request_action import (
    MCPCatalogOAuthRequestAction,
    check_mcp_catalog_o_auth_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="MCPCatalogOAuthRequest")


@_attrs_define
class MCPCatalogOAuthRequest:
    """Account-authorized guided OAuth action bound to an exact role revision."""

    action: MCPCatalogOAuthRequestAction
    role: str
    revision: UUID
    redirect_uri: str | Unset = UNSET
    """Begin only; HTTP IPv4 loopback callback with an explicit port and callback path"""
    state: str | Unset = UNSET
    """Complete only; one-time authorization state"""
    code: str | Unset = UNSET
    """Complete only; authorization code exchanged with sealed PKCE verifier"""
    denied: bool | Unset = UNSET
    """Consume a denied authorization callback without a token exchange"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        role = self.role

        revision = str(self.revision)

        redirect_uri = self.redirect_uri

        state = self.state

        code = self.code

        denied = self.denied

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
                "role": role,
                "revision": revision,
            }
        )
        if redirect_uri is not UNSET:
            field_dict["redirect_uri"] = redirect_uri
        if state is not UNSET:
            field_dict["state"] = state
        if code is not UNSET:
            field_dict["code"] = code
        if denied is not UNSET:
            field_dict["denied"] = denied

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_mcp_catalog_o_auth_request_action(d.pop("action"))

        role = d.pop("role")

        revision = UUID(d.pop("revision"))

        redirect_uri = d.pop("redirect_uri", UNSET)

        state = d.pop("state", UNSET)

        code = d.pop("code", UNSET)

        denied = d.pop("denied", UNSET)

        mcp_catalog_o_auth_request = cls(
            action=action,
            role=role,
            revision=revision,
            redirect_uri=redirect_uri,
            state=state,
            code=code,
            denied=denied,
        )

        mcp_catalog_o_auth_request.additional_properties = d
        return mcp_catalog_o_auth_request

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
