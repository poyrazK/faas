from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_o_auth_authorization_response_state import (
    ToolOAuthAuthorizationResponseState,
    check_tool_o_auth_authorization_response_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ToolOAuthAuthorizationResponse")


@_attrs_define
class ToolOAuthAuthorizationResponse:
    """Owner-only authorization metadata. Begin alone may return a consent URL and state; GET and completion omit them. No
    access token, refresh token, client secret or verifier is returned.

    """

    connection_id: UUID
    state: ToolOAuthAuthorizationResponseState
    authorization_url: str | Unset = UNSET
    """Begin only; private owner browser URL bound to the retained attempt"""
    callback_state: str | Unset = UNSET
    """Begin only; owner callback nonce"""
    expires_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        connection_id = str(self.connection_id)

        state: str = self.state

        authorization_url = self.authorization_url

        callback_state = self.callback_state

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "connection_id": connection_id,
                "state": state,
            }
        )
        if authorization_url is not UNSET:
            field_dict["authorization_url"] = authorization_url
        if callback_state is not UNSET:
            field_dict["callback_state"] = callback_state
        if expires_at is not UNSET:
            field_dict["expires_at"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        connection_id = UUID(d.pop("connection_id"))

        state = check_tool_o_auth_authorization_response_state(d.pop("state"))

        authorization_url = d.pop("authorization_url", UNSET)

        callback_state = d.pop("callback_state", UNSET)

        _expires_at = d.pop("expires_at", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        tool_o_auth_authorization_response = cls(
            connection_id=connection_id,
            state=state,
            authorization_url=authorization_url,
            callback_state=callback_state,
            expires_at=expires_at,
        )

        tool_o_auth_authorization_response.additional_properties = d
        return tool_o_auth_authorization_response

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
