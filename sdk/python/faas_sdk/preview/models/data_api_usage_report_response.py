from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

if TYPE_CHECKING:
    from ..models.data_api_usage_month_to_date import DataAPIUsageMonthToDate


T = TypeVar("T", bound="DataAPIUsageReportResponse")


@_attrs_define
class DataAPIUsageReportResponse:
    """Answer to a runtime usage report."""

    month_to_date: DataAPIUsageMonthToDate
    """Admitted requests in the current UTC calendar month, all and anonymous, which owner caps compare against."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        month_to_date = self.month_to_date.to_dict()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "month_to_date": month_to_date,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_usage_month_to_date import DataAPIUsageMonthToDate

        d = dict(src_dict)
        month_to_date = DataAPIUsageMonthToDate.from_dict(d.pop("month_to_date"))

        data_api_usage_report_response = cls(
            month_to_date=month_to_date,
        )

        data_api_usage_report_response.additional_properties = d
        return data_api_usage_report_response

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
