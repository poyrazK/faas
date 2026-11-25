from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.app_name_report_response_source import AppNameReportResponseSource, check_app_name_report_response_source

if TYPE_CHECKING:
    from ..models.app_name_report_match import AppNameReportMatch


T = TypeVar("T", bound="AppNameReportResponse")


@_attrs_define
class AppNameReportResponse:
    """Existing apps an app name blocklist matches. source is live (app_name_blocklist) or preview (the request's
    blocklist). truncated means more apps matched than were listed.

    """

    source: AppNameReportResponseSource
    entries: int
    """Number of blocklist entries checked."""
    matches: list[AppNameReportMatch]
    truncated: bool
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source: str = self.source

        entries = self.entries

        matches = []
        for matches_item_data in self.matches:
            matches_item = matches_item_data.to_dict()
            matches.append(matches_item)

        truncated = self.truncated

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source": source,
                "entries": entries,
                "matches": matches,
                "truncated": truncated,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.app_name_report_match import AppNameReportMatch

        d = dict(src_dict)
        source = check_app_name_report_response_source(d.pop("source"))

        entries = d.pop("entries")

        matches = []
        _matches = d.pop("matches")
        for matches_item_data in _matches:
            matches_item = AppNameReportMatch.from_dict(matches_item_data)

            matches.append(matches_item)

        truncated = d.pop("truncated")

        app_name_report_response = cls(
            source=source,
            entries=entries,
            matches=matches,
            truncated=truncated,
        )

        app_name_report_response.additional_properties = d
        return app_name_report_response

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
