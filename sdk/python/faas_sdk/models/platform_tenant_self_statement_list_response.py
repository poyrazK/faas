from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.platform_tenant_statement_summary_response import PlatformTenantStatementSummaryResponse


T = TypeVar("T", bound="PlatformTenantSelfStatementListResponse")


@_attrs_define
class PlatformTenantSelfStatementListResponse:
    """Bounded page of finalized statement summaries. next_offset is present only when another page exists."""

    statements: list[PlatformTenantStatementSummaryResponse]
    next_offset: int | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        statements = []
        for statements_item_data in self.statements:
            statements_item = statements_item_data.to_dict()
            statements.append(statements_item)

        next_offset = self.next_offset

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "statements": statements,
            }
        )
        if next_offset is not UNSET:
            field_dict["next_offset"] = next_offset

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.platform_tenant_statement_summary_response import PlatformTenantStatementSummaryResponse

        d = dict(src_dict)
        statements = []
        _statements = d.pop("statements")
        for statements_item_data in _statements:
            statements_item = PlatformTenantStatementSummaryResponse.from_dict(statements_item_data)

            statements.append(statements_item)

        next_offset = d.pop("next_offset", UNSET)

        platform_tenant_self_statement_list_response = cls(
            statements=statements,
            next_offset=next_offset,
        )

        platform_tenant_self_statement_list_response.additional_properties = d
        return platform_tenant_self_statement_list_response

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
