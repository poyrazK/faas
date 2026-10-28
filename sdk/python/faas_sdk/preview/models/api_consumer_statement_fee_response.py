from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.api_consumer_statement_fee_response_kind import (
    APIConsumerStatementFeeResponseKind,
    check_api_consumer_statement_fee_response_kind,
)

T = TypeVar("T", bound="APIConsumerStatementFeeResponse")


@_attrs_define
class APIConsumerStatementFeeResponse:
    """A statement's monthly base fee or minimum-spend top-up."""

    kind: APIConsumerStatementFeeResponseKind
    rate_card_id: UUID
    amount_millicents: int
    """Negative only for a minimum top-up an adjustment reduces because late usage covers it."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        kind: str = self.kind

        rate_card_id = str(self.rate_card_id)

        amount_millicents = self.amount_millicents

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "kind": kind,
                "rate_card_id": rate_card_id,
                "amount_millicents": amount_millicents,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = check_api_consumer_statement_fee_response_kind(d.pop("kind"))

        rate_card_id = UUID(d.pop("rate_card_id"))

        amount_millicents = d.pop("amount_millicents")

        api_consumer_statement_fee_response = cls(
            kind=kind,
            rate_card_id=rate_card_id,
            amount_millicents=amount_millicents,
        )

        api_consumer_statement_fee_response.additional_properties = d
        return api_consumer_statement_fee_response

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
