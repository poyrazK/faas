from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.billing_provider import BillingProvider, check_billing_provider
from ..types import UNSET, Unset

T = TypeVar("T", bound="CreateStatementInvoiceRequest")


@_attrs_define
class CreateStatementInvoiceRequest:
    """Target of a statement invoice export."""

    provider: BillingProvider | Unset = UNSET
    """A billing provider statements export to."""
    customer: str | Unset = UNSET
    """The provider's customer: a Stripe cus_… ID, a Paddle ctm_… ID, or a Lago external customer ID. Omitted, the
    consumer's or tenant's external_ref is used when it is one."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        provider: str | Unset = UNSET
        if not isinstance(self.provider, Unset):
            provider = self.provider

        customer = self.customer

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if provider is not UNSET:
            field_dict["provider"] = provider
        if customer is not UNSET:
            field_dict["customer"] = customer

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        _provider = d.pop("provider", UNSET)
        provider: BillingProvider | Unset
        if isinstance(_provider, Unset):
            provider = UNSET
        else:
            provider = check_billing_provider(_provider)

        customer = d.pop("customer", UNSET)

        create_statement_invoice_request = cls(
            provider=provider,
            customer=customer,
        )

        create_statement_invoice_request.additional_properties = d
        return create_statement_invoice_request

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
