from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="PlatformTenantSpendCapResponse")


@_attrs_define
class PlatformTenantSpendCapResponse:
    """A tenant's spend cap and its last check (ADR-20261019100331)."""

    tenant_id: UUID
    currency: str
    monthly_cap_millicents: int
    capped: bool
    """True while the gateway refuses the tenant this month."""
    created_at: datetime.datetime
    updated_at: datetime.datetime
    month: str | Unset = UNSET
    """UTC month of the tenant's last check."""
    amount_due_millicents: int | Unset = UNSET
    """The month's usage after prepaid credit at the last check; monthly fees are not counted."""
    checked_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        tenant_id = str(self.tenant_id)

        currency = self.currency

        monthly_cap_millicents = self.monthly_cap_millicents

        capped = self.capped

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        month = self.month

        amount_due_millicents = self.amount_due_millicents

        checked_at: str | Unset = UNSET
        if not isinstance(self.checked_at, Unset):
            checked_at = self.checked_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "tenant_id": tenant_id,
                "currency": currency,
                "monthly_cap_millicents": monthly_cap_millicents,
                "capped": capped,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if month is not UNSET:
            field_dict["month"] = month
        if amount_due_millicents is not UNSET:
            field_dict["amount_due_millicents"] = amount_due_millicents
        if checked_at is not UNSET:
            field_dict["checked_at"] = checked_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        tenant_id = UUID(d.pop("tenant_id"))

        currency = d.pop("currency")

        monthly_cap_millicents = d.pop("monthly_cap_millicents")

        capped = d.pop("capped")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        month = d.pop("month", UNSET)

        amount_due_millicents = d.pop("amount_due_millicents", UNSET)

        _checked_at = d.pop("checked_at", UNSET)
        checked_at: datetime.datetime | Unset
        if isinstance(_checked_at, Unset):
            checked_at = UNSET
        else:
            checked_at = datetime.datetime.fromisoformat(_checked_at)

        platform_tenant_spend_cap_response = cls(
            tenant_id=tenant_id,
            currency=currency,
            monthly_cap_millicents=monthly_cap_millicents,
            capped=capped,
            created_at=created_at,
            updated_at=updated_at,
            month=month,
            amount_due_millicents=amount_due_millicents,
            checked_at=checked_at,
        )

        platform_tenant_spend_cap_response.additional_properties = d
        return platform_tenant_spend_cap_response

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
