from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.consumer_billing_statement import ConsumerBillingStatement


T = TypeVar("T", bound="ConsumerBillingStatementsResponse")


@_attrs_define
class ConsumerBillingStatementsResponse:
    """A consumer's latest finalized statements, newest first."""

    statements: list[ConsumerBillingStatement]
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        statements = []
        for statements_item_data in self.statements:
            statements_item = statements_item_data.to_dict()
            statements.append(statements_item)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "statements": statements,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.consumer_billing_statement import ConsumerBillingStatement

        d = dict(src_dict)
        statements = []
        _statements = d.pop("statements")
        for statements_item_data in _statements:
            statements_item = ConsumerBillingStatement.from_dict(statements_item_data)

            statements.append(statements_item)

        consumer_billing_statements_response = cls(
            statements=statements,
        )

        consumer_billing_statements_response.additional_properties = d
        return consumer_billing_statements_response

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
