from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OIDCExchangeResponse")


@_attrs_define
class OIDCExchangeResponse:
    """Body for `POST /v1/auth/oidc/exchange` success response."""

    bearer: str
    """Opaque bearer, format `fp_oidc_<48 hex>`. Use in `Authorization: Bearer …` on routes allowed by the returned
    scope profile."""
    expires_in: int
    """Seconds until the bearer expires (300 today)."""
    token_id: str
    """Opaque row id (UUID). Useful for log correlation / audit reads."""
    scopes: list[str] | Unset = UNSET
    """The fixed scopes granted to this bearer."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        bearer = self.bearer

        expires_in = self.expires_in

        token_id = self.token_id

        scopes: list[str] | Unset = UNSET
        if not isinstance(self.scopes, Unset):
            scopes = self.scopes

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "bearer": bearer,
                "expires_in": expires_in,
                "token_id": token_id,
            }
        )
        if scopes is not UNSET:
            field_dict["scopes"] = scopes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        bearer = d.pop("bearer")

        expires_in = d.pop("expires_in")

        token_id = d.pop("token_id")

        scopes = cast(list[str], d.pop("scopes", UNSET))

        oidc_exchange_response = cls(
            bearer=bearer,
            expires_in=expires_in,
            token_id=token_id,
            scopes=scopes,
        )

        oidc_exchange_response.additional_properties = d
        return oidc_exchange_response

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
