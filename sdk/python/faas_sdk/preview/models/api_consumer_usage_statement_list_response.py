from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.api_consumer_usage_statement_response import APIConsumerUsageStatementResponse


T = TypeVar("T", bound="APIConsumerUsageStatementListResponse")


@_attrs_define
class APIConsumerUsageStatementListResponse:
    """Durable API consumer usage statements, newest period first."""

    statements: list[APIConsumerUsageStatementResponse]
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
        from ..models.api_consumer_usage_statement_response import APIConsumerUsageStatementResponse

        d = dict(src_dict)
        statements = []
        _statements = d.pop("statements")
        for statements_item_data in _statements:
            statements_item = APIConsumerUsageStatementResponse.from_dict(statements_item_data)

            statements.append(statements_item)

        api_consumer_usage_statement_list_response = cls(
            statements=statements,
        )

        api_consumer_usage_statement_list_response.additional_properties = d
        return api_consumer_usage_statement_list_response

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
