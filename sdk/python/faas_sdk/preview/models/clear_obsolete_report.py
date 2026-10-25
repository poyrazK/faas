from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ClearObsoleteReport")


@_attrs_define
class ClearObsoleteReport:
    """Response for POST /v1/apps/{slug}/deployments/clear-obsolete (ADR-124). Count is the number of soft-deleted rows in
    this call; OlderThan echoes the cutoff the store applied (default 168h).

    """

    app_slug: str
    count: int
    older_than: str
    """Echoes the cutoff the store applied to this clear pass (e.g. 168h = 7 days)."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_slug = self.app_slug

        count = self.count

        older_than = self.older_than

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_slug": app_slug,
                "count": count,
                "older_than": older_than,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_slug = d.pop("app_slug")

        count = d.pop("count")

        older_than = d.pop("older_than")

        clear_obsolete_report = cls(
            app_slug=app_slug,
            count=count,
            older_than=older_than,
        )

        clear_obsolete_report.additional_properties = d
        return clear_obsolete_report

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
