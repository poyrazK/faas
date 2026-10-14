from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PutBillingConnectionRequest")


@_attrs_define
class PutBillingConnectionRequest:
    """The account's own key for one billing provider (ADR-1300, ADR-1305)."""

    secret_key: str
    """A Stripe secret or restricted key (sk_… or rk_…), a Paddle API key (pdl_…), or a Lago API key."""
    api_url: str | Unset = UNSET
    """Lago only: the API origin for the EU region or a self-hosted Lago, https:// only; default
    https://api.getlago.com."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secret_key = self.secret_key

        api_url = self.api_url

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secret_key": secret_key,
            }
        )
        if api_url is not UNSET:
            field_dict["api_url"] = api_url

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        secret_key = d.pop("secret_key")

        api_url = d.pop("api_url", UNSET)

        put_billing_connection_request = cls(
            secret_key=secret_key,
            api_url=api_url,
        )

        put_billing_connection_request.additional_properties = d
        return put_billing_connection_request

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
