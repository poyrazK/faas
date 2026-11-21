from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_o_auth_authorization_request_action import (
    ToolOAuthAuthorizationRequestAction,
    check_tool_o_auth_authorization_request_action,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolOAuthAuthorizationRequest")


@_attrs_define
class ToolOAuthAuthorizationRequest:
    """Owner-only transient PKCE command; codes and callback state are never journaled as tool arguments."""

    action: ToolOAuthAuthorizationRequestAction
    redirect_uri: str | Unset = UNSET
    """Begin only; exact http IPv4 loopback callback with a nonzero port and /callback path"""
    state: str | Unset = UNSET
    """Complete only; exact nonce from the pending browser attempt"""
    code: str | Unset = UNSET
    """Complete only; transient provider authorization code"""
    denied: bool | Unset = False
    """Complete a denied browser authorization without a code"""
    issuer: str | Unset = UNSET
    """Optional callback issuer; must equal the immutable configured issuer"""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        action: str = self.action

        redirect_uri = self.redirect_uri

        state = self.state

        code = self.code

        denied = self.denied

        issuer = self.issuer

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "action": action,
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
        if issuer is not UNSET:
            field_dict["issuer"] = issuer

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        action = check_tool_o_auth_authorization_request_action(d.pop("action"))

        redirect_uri = d.pop("redirect_uri", UNSET)

        state = d.pop("state", UNSET)

        code = d.pop("code", UNSET)

        denied = d.pop("denied", UNSET)

        issuer = d.pop("issuer", UNSET)

        tool_o_auth_authorization_request = cls(
            action=action,
            redirect_uri=redirect_uri,
            state=state,
            code=code,
            denied=denied,
            issuer=issuer,
        )

        tool_o_auth_authorization_request.additional_properties = d
        return tool_o_auth_authorization_request

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
