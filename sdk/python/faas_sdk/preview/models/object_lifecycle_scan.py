from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.object_lifecycle_scan_phase import ObjectLifecycleScanPhase, check_object_lifecycle_scan_phase
from ..models.object_lifecycle_scan_state import ObjectLifecycleScanState, check_object_lifecycle_scan_state
from ..types import UNSET, Unset

T = TypeVar("T", bound="ObjectLifecycleScan")


@_attrs_define
class ObjectLifecycleScan:
    """Discovery progress; completed discovery does not imply admitted cleanup has completed."""

    id: UUID
    bucket_id: UUID
    revision: int
    state: ObjectLifecycleScanState
    phase: ObjectLifecycleScanPhase
    scanned_keys: int
    scanned_uploads: int
    created_at: datetime.datetime
    updated_at: datetime.datetime
    finished_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        bucket_id = str(self.bucket_id)

        revision = self.revision

        state: str = self.state

        phase: str = self.phase

        scanned_keys = self.scanned_keys

        scanned_uploads = self.scanned_uploads

        created_at = self.created_at.isoformat()

        updated_at = self.updated_at.isoformat()

        finished_at: str | Unset = UNSET
        if not isinstance(self.finished_at, Unset):
            finished_at = self.finished_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "bucket_id": bucket_id,
                "revision": revision,
                "state": state,
                "phase": phase,
                "scanned_keys": scanned_keys,
                "scanned_uploads": scanned_uploads,
                "created_at": created_at,
                "updated_at": updated_at,
            }
        )
        if finished_at is not UNSET:
            field_dict["finished_at"] = finished_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        bucket_id = UUID(d.pop("bucket_id"))

        revision = d.pop("revision")

        state = check_object_lifecycle_scan_state(d.pop("state"))

        phase = check_object_lifecycle_scan_phase(d.pop("phase"))

        scanned_keys = d.pop("scanned_keys")

        scanned_uploads = d.pop("scanned_uploads")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        updated_at = datetime.datetime.fromisoformat(d.pop("updated_at"))

        _finished_at = d.pop("finished_at", UNSET)
        finished_at: datetime.datetime | Unset
        if isinstance(_finished_at, Unset):
            finished_at = UNSET
        else:
            finished_at = datetime.datetime.fromisoformat(_finished_at)

        object_lifecycle_scan = cls(
            id=id,
            bucket_id=bucket_id,
            revision=revision,
            state=state,
            phase=phase,
            scanned_keys=scanned_keys,
            scanned_uploads=scanned_uploads,
            created_at=created_at,
            updated_at=updated_at,
            finished_at=finished_at,
        )

        return object_lifecycle_scan
