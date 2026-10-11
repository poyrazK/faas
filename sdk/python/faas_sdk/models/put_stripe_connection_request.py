from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PutStripeConnectionRequest")


@_attrs_define
class PutStripeConnectionRequest:
    """The account's own Stripe key for statement invoice exports (ADR-1300)."""

    secret_key: str
    """A Stripe secret or restricted key (sk_… or rk_…); prefer a restricted key limited to invoices and invoice
    items."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        secret_key = self.secret_key

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "secret_key": secret_key,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        secret_key = d.pop("secret_key")

        put_stripe_connection_request = cls(
            secret_key=secret_key,
        )

        put_stripe_connection_request.additional_properties = d
        return put_stripe_connection_request

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
