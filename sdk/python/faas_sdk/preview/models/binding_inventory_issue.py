from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.binding_inventory_issue_severity import (
    BindingInventoryIssueSeverity,
    check_binding_inventory_issue_severity,
)

T = TypeVar("T", bound="BindingInventoryIssue")


@_attrs_define
class BindingInventoryIssue:
    """Stable and sanitized reason why a binding inventory section could not be read."""

    type_: str
    """Binding family or runtime_freshness or binding_refresh whose read was incomplete."""
    code: str
    """Stable reason such as forbidden, unavailable, query_failed, consumer_status_unavailable or
    managed_postgres_unavailable."""
    severity: BindingInventoryIssueSeverity
    message: str
    """Sanitized explanation without raw provider errors."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        type_ = self.type_

        code = self.code

        severity: str = self.severity

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "type": type_,
                "code": code,
                "severity": severity,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        type_ = d.pop("type")

        code = d.pop("code")

        severity = check_binding_inventory_issue_severity(d.pop("severity"))

        message = d.pop("message")

        binding_inventory_issue = cls(
            type_=type_,
            code=code,
            severity=severity,
            message=message,
        )

        binding_inventory_issue.additional_properties = d
        return binding_inventory_issue

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
