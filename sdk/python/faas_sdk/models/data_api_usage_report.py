from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_usage_report_version import DataAPIUsageReportVersion, check_data_api_usage_report_version

if TYPE_CHECKING:
    from ..models.data_api_usage_hour import DataAPIUsageHour


T = TypeVar("T", bound="DataAPIUsageReport")


@_attrs_define
class DataAPIUsageReport:
    """Private versioned runtime usage report. A report id names the reporting instance and process and is applied once;
    hours are unique.

    """

    version: DataAPIUsageReportVersion
    report_id: str
    hours: list[DataAPIUsageHour]

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        report_id = self.report_id

        hours = []
        for hours_item_data in self.hours:
            hours_item = hours_item_data.to_dict()
            hours.append(hours_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "report_id": report_id,
                "hours": hours,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_usage_hour import DataAPIUsageHour

        d = dict(src_dict)
        version = check_data_api_usage_report_version(d.pop("version"))

        report_id = d.pop("report_id")

        hours = []
        _hours = d.pop("hours")
        for hours_item_data in _hours:
            hours_item = DataAPIUsageHour.from_dict(hours_item_data)

            hours.append(hours_item)

        data_api_usage_report = cls(
            version=version,
            report_id=report_id,
            hours=hours,
        )

        return data_api_usage_report
