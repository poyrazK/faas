from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_api_usage_report_version import DataAPIUsageReportVersion, check_data_api_usage_report_version
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_api_access_config import DataAPIAccessConfig
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
    access: DataAPIAccessConfig | Unset = UNSET
    """Access settings the runtime reports for live reads without a user token, sent after start and hourly. It
    replaces the stored settings; turning usage reporting off forgets them. Holds key hashes, never keys."""

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        report_id = self.report_id

        hours = []
        for hours_item_data in self.hours:
            hours_item = hours_item_data.to_dict()
            hours.append(hours_item)

        access: dict[str, Any] | Unset = UNSET
        if not isinstance(self.access, Unset):
            access = self.access.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "report_id": report_id,
                "hours": hours,
            }
        )
        if access is not UNSET:
            field_dict["access"] = access

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_api_access_config import DataAPIAccessConfig
        from ..models.data_api_usage_hour import DataAPIUsageHour

        d = dict(src_dict)
        version = check_data_api_usage_report_version(d.pop("version"))

        report_id = d.pop("report_id")

        hours = []
        _hours = d.pop("hours")
        for hours_item_data in _hours:
            hours_item = DataAPIUsageHour.from_dict(hours_item_data)

            hours.append(hours_item)

        _access = d.pop("access", UNSET)
        access: DataAPIAccessConfig | Unset
        if isinstance(_access, Unset):
            access = UNSET
        else:
            access = DataAPIAccessConfig.from_dict(_access)

        data_api_usage_report = cls(
            version=version,
            report_id=report_id,
            hours=hours,
            access=access,
        )

        return data_api_usage_report
