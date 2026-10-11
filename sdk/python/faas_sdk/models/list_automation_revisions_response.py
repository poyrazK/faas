from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

if TYPE_CHECKING:
    from ..models.automation_revision_response import AutomationRevisionResponse


T = TypeVar("T", bound="ListAutomationRevisionsResponse")


@_attrs_define
class ListAutomationRevisionsResponse:
    """Newest-first immutable publication history and pagination metadata."""

    revisions: list[AutomationRevisionResponse]
    total: int
    limit: int
    offset: int

    def to_dict(self) -> dict[str, Any]:
        revisions = []
        for revisions_item_data in self.revisions:
            revisions_item = revisions_item_data.to_dict()
            revisions.append(revisions_item)

        total = self.total

        limit = self.limit

        offset = self.offset

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "revisions": revisions,
                "total": total,
                "limit": limit,
                "offset": offset,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.automation_revision_response import AutomationRevisionResponse

        d = dict(src_dict)
        revisions = []
        _revisions = d.pop("revisions")
        for revisions_item_data in _revisions:
            revisions_item = AutomationRevisionResponse.from_dict(revisions_item_data)

            revisions.append(revisions_item)

        total = d.pop("total")

        limit = d.pop("limit")

        offset = d.pop("offset")

        list_automation_revisions_response = cls(
            revisions=revisions,
            total=total,
            limit=limit,
            offset=offset,
        )

        return list_automation_revisions_response
