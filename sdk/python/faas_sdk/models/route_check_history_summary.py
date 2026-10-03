from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.route_check_history_summary_comparison_status import (
    RouteCheckHistorySummaryComparisonStatus,
    check_route_check_history_summary_comparison_status,
)
from ..models.route_check_history_summary_status import (
    RouteCheckHistorySummaryStatus,
    check_route_check_history_summary_status,
)

if TYPE_CHECKING:
    from ..models.route_check_change_summary import RouteCheckChangeSummary


T = TypeVar("T", bound="RouteCheckHistorySummary")


@_attrs_define
class RouteCheckHistorySummary:
    """Compact completion metadata; use its ID to read retained full evidence."""

    version: int
    id: UUID
    checked_at: datetime.datetime
    status: RouteCheckHistorySummaryStatus
    requirements_revision: int
    requirements_sha256: str
    comparison_status: RouteCheckHistorySummaryComparisonStatus
    summary: RouteCheckChangeSummary
    """Exact finding-change counts across the bounded comparison, including observations omitted from truncated
    detail."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        id = str(self.id)

        checked_at = self.checked_at.isoformat()

        status: str = self.status

        requirements_revision = self.requirements_revision

        requirements_sha256 = self.requirements_sha256

        comparison_status: str = self.comparison_status

        summary = self.summary.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "version": version,
                "id": id,
                "checked_at": checked_at,
                "status": status,
                "requirements_revision": requirements_revision,
                "requirements_sha256": requirements_sha256,
                "comparison_status": comparison_status,
                "summary": summary,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.route_check_change_summary import RouteCheckChangeSummary

        d = dict(src_dict)
        version = d.pop("version")

        id = UUID(d.pop("id"))

        checked_at = datetime.datetime.fromisoformat(d.pop("checked_at"))

        status = check_route_check_history_summary_status(d.pop("status"))

        requirements_revision = d.pop("requirements_revision")

        requirements_sha256 = d.pop("requirements_sha256")

        comparison_status = check_route_check_history_summary_comparison_status(d.pop("comparison_status"))

        summary = RouteCheckChangeSummary.from_dict(d.pop("summary"))

        route_check_history_summary = cls(
            version=version,
            id=id,
            checked_at=checked_at,
            status=status,
            requirements_revision=requirements_revision,
            requirements_sha256=requirements_sha256,
            comparison_status=comparison_status,
            summary=summary,
        )

        route_check_history_summary.additional_properties = d
        return route_check_history_summary

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
