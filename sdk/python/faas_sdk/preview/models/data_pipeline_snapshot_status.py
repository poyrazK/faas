from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.data_pipeline_snapshot_status_state import (
    DataPipelineSnapshotStatusState,
    check_data_pipeline_snapshot_status_state,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="DataPipelineSnapshotStatus")


@_attrs_define
class DataPipelineSnapshotStatus:
    """Most recent snapshot attempt plus the last activated capture. Rows and bytes describe that successful capture and
    remain unchanged after failed refreshes. A private target receipt preserves capture time through lost
    acknowledgements.

    """

    run_id: str
    state: DataPipelineSnapshotStatusState
    rows: int
    bytes_: int
    reason: str | Unset = UNSET
    started_at: datetime.datetime | Unset = UNSET
    next_run_at: datetime.datetime | Unset = UNSET
    captured_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        run_id = self.run_id

        state: str = self.state

        rows = self.rows

        bytes_ = self.bytes_

        reason = self.reason

        started_at: str | Unset = UNSET
        if not isinstance(self.started_at, Unset):
            started_at = self.started_at.isoformat()

        next_run_at: str | Unset = UNSET
        if not isinstance(self.next_run_at, Unset):
            next_run_at = self.next_run_at.isoformat()

        captured_at: str | Unset = UNSET
        if not isinstance(self.captured_at, Unset):
            captured_at = self.captured_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "run_id": run_id,
                "state": state,
                "rows": rows,
                "bytes": bytes_,
            }
        )
        if reason is not UNSET:
            field_dict["reason"] = reason
        if started_at is not UNSET:
            field_dict["started_at"] = started_at
        if next_run_at is not UNSET:
            field_dict["next_run_at"] = next_run_at
        if captured_at is not UNSET:
            field_dict["captured_at"] = captured_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = d.pop("run_id")

        state = check_data_pipeline_snapshot_status_state(d.pop("state"))

        rows = d.pop("rows")

        bytes_ = d.pop("bytes")

        reason = d.pop("reason", UNSET)

        _started_at = d.pop("started_at", UNSET)
        started_at: datetime.datetime | Unset
        if isinstance(_started_at, Unset):
            started_at = UNSET
        else:
            started_at = datetime.datetime.fromisoformat(_started_at)

        _next_run_at = d.pop("next_run_at", UNSET)
        next_run_at: datetime.datetime | Unset
        if isinstance(_next_run_at, Unset):
            next_run_at = UNSET
        else:
            next_run_at = datetime.datetime.fromisoformat(_next_run_at)

        _captured_at = d.pop("captured_at", UNSET)
        captured_at: datetime.datetime | Unset
        if isinstance(_captured_at, Unset):
            captured_at = UNSET
        else:
            captured_at = datetime.datetime.fromisoformat(_captured_at)

        data_pipeline_snapshot_status = cls(
            run_id=run_id,
            state=state,
            rows=rows,
            bytes_=bytes_,
            reason=reason,
            started_at=started_at,
            next_run_at=next_run_at,
            captured_at=captured_at,
        )

        return data_pipeline_snapshot_status
