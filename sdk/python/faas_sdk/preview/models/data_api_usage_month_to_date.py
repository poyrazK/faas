from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="DataAPIUsageMonthToDate")


@_attrs_define
class DataAPIUsageMonthToDate:
    """Admitted requests in the current UTC calendar month, all and anonymous, which owner caps compare against."""

    month: str
    """UTC calendar month as YYYY-MM."""
    requests: int
    anonymous_requests: int
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        month = self.month

        requests = self.requests

        anonymous_requests = self.anonymous_requests

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "month": month,
                "requests": requests,
                "anonymous_requests": anonymous_requests,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        month = d.pop("month")

        requests = d.pop("requests")

        anonymous_requests = d.pop("anonymous_requests")

        data_api_usage_month_to_date = cls(
            month=month,
            requests=requests,
            anonymous_requests=anonymous_requests,
        )

        data_api_usage_month_to_date.additional_properties = d
        return data_api_usage_month_to_date

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
