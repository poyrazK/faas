from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantStatementLineResponse")


@_attrs_define
class PlatformTenantStatementLineResponse:
    """Immutable tenant-attributed UTC minute priced with one app's rate-card version."""

    app_id: UUID
    consumer_id: UUID
    window_start: datetime.datetime
    billable_units: int
    amount_millicents: int
    rate_card_id: UUID | Unset = UNSET
    currency: str | Unset = UNSET
    price_millicents_per_unit: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = str(self.app_id)

        consumer_id = str(self.consumer_id)

        window_start = self.window_start.isoformat()

        billable_units = self.billable_units

        amount_millicents = self.amount_millicents

        rate_card_id: str | Unset = UNSET
        if not isinstance(self.rate_card_id, Unset):
            rate_card_id = str(self.rate_card_id)

        currency = self.currency

        price_millicents_per_unit = self.price_millicents_per_unit

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "consumer_id": consumer_id,
                "window_start": window_start,
                "billable_units": billable_units,
                "amount_millicents": amount_millicents,
            }
        )
        if rate_card_id is not UNSET:
            field_dict["rate_card_id"] = rate_card_id
        if currency is not UNSET:
            field_dict["currency"] = currency
        if price_millicents_per_unit is not UNSET:
            field_dict["price_millicents_per_unit"] = price_millicents_per_unit

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = UUID(d.pop("app_id"))

        consumer_id = UUID(d.pop("consumer_id"))

        window_start = datetime.datetime.fromisoformat(d.pop("window_start"))

        billable_units = d.pop("billable_units")

        amount_millicents = d.pop("amount_millicents")

        _rate_card_id = d.pop("rate_card_id", UNSET)
        rate_card_id: UUID | Unset
        if isinstance(_rate_card_id, Unset):
            rate_card_id = UNSET
        else:
            rate_card_id = UUID(_rate_card_id)

        currency = d.pop("currency", UNSET)

        price_millicents_per_unit = d.pop("price_millicents_per_unit", UNSET)

        platform_tenant_statement_line_response = cls(
            app_id=app_id,
            consumer_id=consumer_id,
            window_start=window_start,
            billable_units=billable_units,
            amount_millicents=amount_millicents,
            rate_card_id=rate_card_id,
            currency=currency,
            price_millicents_per_unit=price_millicents_per_unit,
        )

        platform_tenant_statement_line_response.additional_properties = d
        return platform_tenant_statement_line_response

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
