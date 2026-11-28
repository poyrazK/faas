from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.tool_publication_verification_outcome_checks_item_status import (
    ToolPublicationVerificationOutcomeChecksItemStatus,
    check_tool_publication_verification_outcome_checks_item_status,
)

T = TypeVar("T", bound="ToolPublicationVerificationOutcomeChecksItem")


@_attrs_define
class ToolPublicationVerificationOutcomeChecksItem:
    name: str
    status: ToolPublicationVerificationOutcomeChecksItemStatus
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        status: str = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "name": name,
                "status": status,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        name = d.pop("name")

        status = check_tool_publication_verification_outcome_checks_item_status(d.pop("status"))

        tool_publication_verification_outcome_checks_item = cls(
            name=name,
            status=status,
        )

        tool_publication_verification_outcome_checks_item.additional_properties = d
        return tool_publication_verification_outcome_checks_item

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
