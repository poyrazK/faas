from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_billing_statement_progress_status import (
    APIConsumerBillingStatementProgressStatus,
    check_api_consumer_billing_statement_progress_status,
)

T = TypeVar("T", bound="APIConsumerBillingStatementProgress")


@_attrs_define
class APIConsumerBillingStatementProgress:
    """Statement associated with the chosen invoice, or the most relevant statement to review."""

    id: UUID
    status: APIConsumerBillingStatementProgressStatus
    amount_due_millicents: int
    unpriced_units: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        status: str = self.status

        amount_due_millicents = self.amount_due_millicents

        unpriced_units = self.unpriced_units

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "id": id,
                "status": status,
                "amount_due_millicents": amount_due_millicents,
                "unpriced_units": unpriced_units,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        status = check_api_consumer_billing_statement_progress_status(d.pop("status"))

        amount_due_millicents = d.pop("amount_due_millicents")

        unpriced_units = d.pop("unpriced_units")

        api_consumer_billing_statement_progress = cls(
            id=id,
            status=status,
            amount_due_millicents=amount_due_millicents,
            unpriced_units=unpriced_units,
        )

        api_consumer_billing_statement_progress.additional_properties = d
        return api_consumer_billing_statement_progress

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
