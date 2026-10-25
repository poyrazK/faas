from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.issue_breadcrumb_category import IssueBreadcrumbCategory, check_issue_breadcrumb_category
from ..models.issue_breadcrumb_level import IssueBreadcrumbLevel, check_issue_breadcrumb_level

T = TypeVar("T", bound="IssueBreadcrumb")


@_attrs_define
class IssueBreadcrumb:
    """Bounded sanitized event context recorded before an issue occurrence."""

    occurred_at: datetime.datetime
    category: IssueBreadcrumbCategory
    level: IssueBreadcrumbLevel
    message: str
    """Sanitized and bounded to 256 UTF-8 bytes."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        occurred_at = self.occurred_at.isoformat()

        category: str = self.category

        level: str = self.level

        message = self.message

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "occurred_at": occurred_at,
                "category": category,
                "level": level,
                "message": message,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        occurred_at = datetime.datetime.fromisoformat(d.pop("occurred_at"))

        category = check_issue_breadcrumb_category(d.pop("category"))

        level = check_issue_breadcrumb_level(d.pop("level"))

        message = d.pop("message")

        issue_breadcrumb = cls(
            occurred_at=occurred_at,
            category=category,
            level=level,
            message=message,
        )

        issue_breadcrumb.additional_properties = d
        return issue_breadcrumb

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
