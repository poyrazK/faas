from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.o_auth_token_exchange_response_issued_token_type import (
    OAuthTokenExchangeResponseIssuedTokenType,
    check_o_auth_token_exchange_response_issued_token_type,
)
from ..models.o_auth_token_exchange_response_scope import (
    OAuthTokenExchangeResponseScope,
    check_o_auth_token_exchange_response_scope,
)
from ..models.o_auth_token_exchange_response_token_type import (
    OAuthTokenExchangeResponseTokenType,
    check_o_auth_token_exchange_response_token_type,
)

T = TypeVar("T", bound="OAuthTokenExchangeResponse")


@_attrs_define
class OAuthTokenExchangeResponse:
    """RFC 8693 token exchange response with an opaque deploy bearer."""

    access_token: str
    """Opaque `fp_oidc_…` bearer for deploy routes."""
    issued_token_type: OAuthTokenExchangeResponseIssuedTokenType
    token_type: OAuthTokenExchangeResponseTokenType
    expires_in: int
    """Seconds until the bearer expires."""
    scope: OAuthTokenExchangeResponseScope
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        access_token = self.access_token

        issued_token_type: str = self.issued_token_type

        token_type: str = self.token_type

        expires_in = self.expires_in

        scope: str = self.scope

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "access_token": access_token,
                "issued_token_type": issued_token_type,
                "token_type": token_type,
                "expires_in": expires_in,
                "scope": scope,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        access_token = d.pop("access_token")

        issued_token_type = check_o_auth_token_exchange_response_issued_token_type(d.pop("issued_token_type"))

        token_type = check_o_auth_token_exchange_response_token_type(d.pop("token_type"))

        expires_in = d.pop("expires_in")

        scope = check_o_auth_token_exchange_response_scope(d.pop("scope"))

        o_auth_token_exchange_response = cls(
            access_token=access_token,
            issued_token_type=issued_token_type,
            token_type=token_type,
            expires_in=expires_in,
            scope=scope,
        )

        o_auth_token_exchange_response.additional_properties = d
        return o_auth_token_exchange_response

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
