from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.issue_saved_view_filters import IssueSavedViewFilters


T = TypeVar("T", bound="PutIssueSavedViewRequest")


@_attrs_define
class PutIssueSavedViewRequest:
    """Shared issue-view display name and validated replacement filter set."""

    name: str
    """Trimmed display name"""
    filters: IssueSavedViewFilters
    """Supported query filters retained by a shared application issue view."""

    def to_dict(self) -> dict[str, Any]:
        name = self.name

        filters = self.filters.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "name": name,
                "filters": filters,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.issue_saved_view_filters import IssueSavedViewFilters

        d = dict(src_dict)
        name = d.pop("name")

        filters = IssueSavedViewFilters.from_dict(d.pop("filters"))

        put_issue_saved_view_request = cls(
            name=name,
            filters=filters,
        )

        return put_issue_saved_view_request
