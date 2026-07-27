from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="UsageResponse")


@_attrs_define
class UsageResponse:
    """Per-app usage for one month: GB-hours consumed and a `next_before` cursor for paging deployments within the window."""

    app_id: str
    mb_seconds: int
    requests: int
    included_gb_hours: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        app_id = self.app_id

        mb_seconds = self.mb_seconds

        requests = self.requests

        included_gb_hours = self.included_gb_hours

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "app_id": app_id,
                "mb_seconds": mb_seconds,
                "requests": requests,
                "included_gb_hours": included_gb_hours,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        app_id = d.pop("app_id")

        mb_seconds = d.pop("mb_seconds")

        requests = d.pop("requests")

        included_gb_hours = d.pop("included_gb_hours")

        usage_response = cls(
            app_id=app_id,
            mb_seconds=mb_seconds,
            requests=requests,
            included_gb_hours=included_gb_hours,
        )

        usage_response.additional_properties = d
        return usage_response

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
