from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="DataPipelineWorkWindow")


@_attrs_define
class DataPipelineWorkWindow:
    """Daily UTC minute window with an inclusive start and exclusive end. Start and end differ; a start after end crosses
    midnight. Omit the window for all-day work.

    """

    start_minute_utc: int
    end_minute_utc: int

    def to_dict(self) -> dict[str, Any]:
        start_minute_utc = self.start_minute_utc

        end_minute_utc = self.end_minute_utc

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "start_minute_utc": start_minute_utc,
                "end_minute_utc": end_minute_utc,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        start_minute_utc = d.pop("start_minute_utc")

        end_minute_utc = d.pop("end_minute_utc")

        data_pipeline_work_window = cls(
            start_minute_utc=start_minute_utc,
            end_minute_utc=end_minute_utc,
        )

        return data_pipeline_work_window
