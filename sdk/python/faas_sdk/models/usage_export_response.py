from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UsageExportResponse")


@_attrs_define
class UsageExportResponse:
    """One usage record: app id, GB-hours consumed, started/finished timestamps for the export window."""

    app_id: str
    month: str
    mb_seconds: int
    requests: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        month = self.month

        mb_seconds = self.mb_seconds

        requests = self.requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "month": month,
                "mb_seconds": mb_seconds,
                "requests": requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        month = d.pop("month")

        mb_seconds = d.pop("mb_seconds")

        requests = d.pop("requests")

        usage_export_response = cls(
            app_id=app_id,
            month=month,
            mb_seconds=mb_seconds,
            requests=requests,
        )

        usage_export_response.additional_properties = d
        return usage_export_response

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
