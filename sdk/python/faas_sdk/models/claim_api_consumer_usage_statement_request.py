from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ClaimAPIConsumerUsageStatementRequest")


@_attrs_define
class ClaimAPIConsumerUsageStatementRequest:
    """Customer billing system's external invoice reference for a finalized statement."""

    external_invoice_id: str
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        external_invoice_id = self.external_invoice_id

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "external_invoice_id": external_invoice_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        external_invoice_id = d.pop("external_invoice_id")

        claim_api_consumer_usage_statement_request = cls(
            external_invoice_id=external_invoice_id,
        )

        claim_api_consumer_usage_statement_request.additional_properties = d
        return claim_api_consumer_usage_statement_request

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
