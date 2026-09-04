from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.admin_refund_response_provider import AdminRefundResponseProvider, check_admin_refund_response_provider

T = TypeVar("T", bound="AdminRefundResponse")


@_attrs_define
class AdminRefundResponse:
    """Result of an operator-initiated Polar refund. The local invoice and
    provider refund identifiers are both returned for reconciliation.
    Amounts are integer EUR cents.

    """

    account_id: UUID
    invoice_id: UUID
    provider: AdminRefundResponseProvider
    provider_refund_id: str
    charge_id: str
    """Polar order ID used by the refund."""
    amount_cents: int
    currency: str
    status: str
    """Provider refund status."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        account_id = str(self.account_id)

        invoice_id = str(self.invoice_id)

        provider: str = self.provider

        provider_refund_id = self.provider_refund_id

        charge_id = self.charge_id

        amount_cents = self.amount_cents

        currency = self.currency

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "account_id": account_id,
                "invoice_id": invoice_id,
                "provider": provider,
                "provider_refund_id": provider_refund_id,
                "charge_id": charge_id,
                "amount_cents": amount_cents,
                "currency": currency,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        account_id = UUID(d.pop("account_id"))

        invoice_id = UUID(d.pop("invoice_id"))

        provider = check_admin_refund_response_provider(d.pop("provider"))

        provider_refund_id = d.pop("provider_refund_id")

        charge_id = d.pop("charge_id")

        amount_cents = d.pop("amount_cents")

        currency = d.pop("currency")

        status = d.pop("status")

        admin_refund_response = cls(
            account_id=account_id,
            invoice_id=invoice_id,
            provider=provider,
            provider_refund_id=provider_refund_id,
            charge_id=charge_id,
            amount_cents=amount_cents,
            currency=currency,
            status=status,
        )

        admin_refund_response.additional_properties = d
        return admin_refund_response

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
