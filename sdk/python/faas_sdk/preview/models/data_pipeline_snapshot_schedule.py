from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineSnapshotSchedule")


@_attrs_define
class DataPipelineSnapshotSchedule:
    """Read-only full snapshot cadence. Zero row/byte budgets select the pilot ceilings of 100000 rows and 64 MiB. Runs are
    bounded to 45 seconds and retain the last successful capture on failure.

    """

    every_seconds: int
    max_rows: int | Unset = UNSET
    max_bytes: int | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        every_seconds = self.every_seconds

        max_rows = self.max_rows

        max_bytes = self.max_bytes

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "every_seconds": every_seconds,
            }
        )
        if max_rows is not UNSET:
            field_dict["max_rows"] = max_rows
        if max_bytes is not UNSET:
            field_dict["max_bytes"] = max_bytes

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        every_seconds = d.pop("every_seconds")

        max_rows = d.pop("max_rows", UNSET)

        max_bytes = d.pop("max_bytes", UNSET)

        data_pipeline_snapshot_schedule = cls(
            every_seconds=every_seconds,
            max_rows=max_rows,
            max_bytes=max_bytes,
        )

        return data_pipeline_snapshot_schedule
