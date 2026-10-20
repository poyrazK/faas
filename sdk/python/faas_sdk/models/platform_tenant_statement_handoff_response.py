from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="PlatformTenantStatementHandoffResponse")


@_attrs_define
class PlatformTenantStatementHandoffResponse:
    """Provider-neutral receipt for a finalized cross-app statement revision."""

    id: UUID
    statement_id: UUID
    external_invoice_id: str
    currency: str
    amount_millicents: int
    created_at: datetime.datetime
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        statement_id = str(self.statement_id)

        external_invoice_id = self.external_invoice_id

        currency = self.currency

        amount_millicents = self.amount_millicents

        created_at = self.created_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "statement_id": statement_id,
                "external_invoice_id": external_invoice_id,
                "currency": currency,
                "amount_millicents": amount_millicents,
                "created_at": created_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        statement_id = UUID(d.pop("statement_id"))

        external_invoice_id = d.pop("external_invoice_id")

        currency = d.pop("currency")

        amount_millicents = d.pop("amount_millicents")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        platform_tenant_statement_handoff_response = cls(
            id=id,
            statement_id=statement_id,
            external_invoice_id=external_invoice_id,
            currency=currency,
            amount_millicents=amount_millicents,
            created_at=created_at,
        )

        platform_tenant_statement_handoff_response.additional_properties = d
        return platform_tenant_statement_handoff_response

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
