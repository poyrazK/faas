from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_operations_version import (
    DataPipelineOperationsVersion,
    check_data_pipeline_operations_version,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_quality_schedule import DataPipelineQualitySchedule
    from ..models.data_pipeline_snapshot_schedule import DataPipelineSnapshotSchedule
    from ..models.data_pipeline_workload import DataPipelineWorkload


T = TypeVar("T", bound="DataPipelineOperations")


@_attrs_define
class DataPipelineOperations:
    """Versioned policy for bounded scheduled snapshots, mirror quality checks and optional maintenance. Snapshot mode is
    chosen at creation and cannot be changed later.

    """

    version: DataPipelineOperationsVersion
    snapshot: DataPipelineSnapshotSchedule | Unset = UNSET
    """Read-only full snapshot cadence. Zero row/byte budgets select the pilot ceilings of 100000 rows and 64 MiB.
    Runs are bounded to 45 seconds and retain the last successful capture on failure."""
    quality: DataPipelineQualitySchedule | Unset = UNSET
    """Scheduled curated mirror checks, limited to 16 named rules and 10000 inspected source rows per rule.
    Incomplete coverage cannot be reported as passed; only counts and status leave the private worker."""
    workload: DataPipelineWorkload | Unset = UNSET
    """Owner maintenance budgets within hard pilot bounds. Query timeout is zero for the default or 50 through 2000
    milliseconds; batch count is zero for default or one through four. Native COPY bandwidth is outside this policy.
   """

    def to_dict(self) -> dict[str, Any]:
        version: int = self.version

        snapshot: dict[str, Any] | Unset = UNSET
        if not isinstance(self.snapshot, Unset):
            snapshot = self.snapshot.to_dict()

        quality: dict[str, Any] | Unset = UNSET
        if not isinstance(self.quality, Unset):
            quality = self.quality.to_dict()

        workload: dict[str, Any] | Unset = UNSET
        if not isinstance(self.workload, Unset):
            workload = self.workload.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
            }
        )
        if snapshot is not UNSET:
            field_dict["snapshot"] = snapshot
        if quality is not UNSET:
            field_dict["quality"] = quality
        if workload is not UNSET:
            field_dict["workload"] = workload

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_quality_schedule import DataPipelineQualitySchedule
        from ..models.data_pipeline_snapshot_schedule import DataPipelineSnapshotSchedule
        from ..models.data_pipeline_workload import DataPipelineWorkload

        d = dict(src_dict)
        version = check_data_pipeline_operations_version(d.pop("version"))

        _snapshot = d.pop("snapshot", UNSET)
        snapshot: DataPipelineSnapshotSchedule | Unset
        if isinstance(_snapshot, Unset):
            snapshot = UNSET
        else:
            snapshot = DataPipelineSnapshotSchedule.from_dict(_snapshot)

        _quality = d.pop("quality", UNSET)
        quality: DataPipelineQualitySchedule | Unset
        if isinstance(_quality, Unset):
            quality = UNSET
        else:
            quality = DataPipelineQualitySchedule.from_dict(_quality)

        _workload = d.pop("workload", UNSET)
        workload: DataPipelineWorkload | Unset
        if isinstance(_workload, Unset):
            workload = UNSET
        else:
            workload = DataPipelineWorkload.from_dict(_workload)

        data_pipeline_operations = cls(
            version=version,
            snapshot=snapshot,
            quality=quality,
            workload=workload,
        )

        return data_pipeline_operations
