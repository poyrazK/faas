from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.data_pipeline_quality_status import DataPipelineQualityStatus
    from ..models.data_pipeline_snapshot_status import DataPipelineSnapshotStatus


T = TypeVar("T", bound="DataPipelineOperationsStatus")


@_attrs_define
class DataPipelineOperationsStatus:
    """Historical scheduled-work observations. Capture time bounds data age independently of the catalog worker observation
    timestamp.

    """

    snapshot: DataPipelineSnapshotStatus | Unset = UNSET
    """Most recent snapshot attempt plus the last activated capture. Rows and bytes describe that successful
    capture and remain unchanged after failed refreshes. A private target receipt preserves capture time through
    lost acknowledgements."""
    quality: DataPipelineQualityStatus | Unset = UNSET
    """Last completed or attempted quality scan for the recorded generation. Unknown and inconclusive are separate
    from passed; failing row values never appear in this observation."""

    def to_dict(self) -> dict[str, Any]:
        snapshot: dict[str, Any] | Unset = UNSET
        if not isinstance(self.snapshot, Unset):
            snapshot = self.snapshot.to_dict()

        quality: dict[str, Any] | Unset = UNSET
        if not isinstance(self.quality, Unset):
            quality = self.quality.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update({})
        if snapshot is not UNSET:
            field_dict["snapshot"] = snapshot
        if quality is not UNSET:
            field_dict["quality"] = quality

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.data_pipeline_quality_status import DataPipelineQualityStatus
        from ..models.data_pipeline_snapshot_status import DataPipelineSnapshotStatus

        d = dict(src_dict)
        _snapshot = d.pop("snapshot", UNSET)
        snapshot: DataPipelineSnapshotStatus | Unset
        if isinstance(_snapshot, Unset):
            snapshot = UNSET
        else:
            snapshot = DataPipelineSnapshotStatus.from_dict(_snapshot)

        _quality = d.pop("quality", UNSET)
        quality: DataPipelineQualityStatus | Unset
        if isinstance(_quality, Unset):
            quality = UNSET
        else:
            quality = DataPipelineQualityStatus.from_dict(_quality)

        data_pipeline_operations_status = cls(
            snapshot=snapshot,
            quality=quality,
        )

        return data_pipeline_operations_status
