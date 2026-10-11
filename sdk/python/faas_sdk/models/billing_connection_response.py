from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_connection_response_provider import (
    BillingConnectionResponseProvider,
    check_billing_connection_response_provider,
)

T = TypeVar("T", bound="BillingConnectionResponse")


@_attrs_define
class BillingConnectionResponse:
    """A stored payment-provider key, identified without revealing it."""

    provider: BillingConnectionResponseProvider
    key_last4: str
    livemode: bool
    created_at: datetime.datetime
    updated_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str = self.provider

        key_last4 = self.key_last4

        livemode = self.livemode

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "provider": provider,
                "key_last4": key_last4,
                "livemode": livemode,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        provider = check_billing_connection_response_provider(d.pop("provider"))

        key_last4 = d.pop("key_last4")

        livemode = d.pop("livemode")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        billing_connection_response = cls(
            provider=provider,
            key_last4=key_last4,
            livemode=livemode,
            created_at=created_at,
            updated_at=updated_at,
        )

        billing_connection_response.additional_properties = d
        return billing_connection_response

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
