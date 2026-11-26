from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_second_invoice_review_status import (
    APISecondInvoiceReviewStatus,
    check_api_second_invoice_review_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="APISecondInvoiceReview")


@_attrs_define
class APISecondInvoiceReview:
    """Next-cycle statement and billing status for an app consumer with a confirmed first payment."""

    consumer_id: UUID
    name: str
    """App customer display name."""
    month: str
    """UTC month of the selected statement."""
    statement_id: UUID
    status: APISecondInvoiceReviewStatus
    hold_run_id: UUID | Unset = UNSET
    """Completed draft run with the active usage hold."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        consumer_id = str(self.consumer_id)

        name = self.name

        month = self.month

        statement_id = str(self.statement_id)

        status: str = self.status

        hold_run_id: str | Unset = UNSET
        if not isinstance(self.hold_run_id, Unset):
            hold_run_id = str(self.hold_run_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "consumer_id": consumer_id,
                "name": name,
                "month": month,
                "statement_id": statement_id,
                "status": status,
            }
        )
        if hold_run_id is not UNSET:
            field_dict["hold_run_id"] = hold_run_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        consumer_id = UUID(d.pop("consumer_id"))

        name = d.pop("name")

        month = d.pop("month")

        statement_id = UUID(d.pop("statement_id"))

        status = check_api_second_invoice_review_status(d.pop("status"))

        _hold_run_id = d.pop("hold_run_id", UNSET)
        hold_run_id: UUID | Unset
        if isinstance(_hold_run_id, Unset):
            hold_run_id = UNSET
        else:
            hold_run_id = UUID(_hold_run_id)

        api_second_invoice_review = cls(
            consumer_id=consumer_id,
            name=name,
            month=month,
            statement_id=statement_id,
            status=status,
            hold_run_id=hold_run_id,
        )

        api_second_invoice_review.additional_properties = d
        return api_second_invoice_review

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
