from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="ProjectRollbackSummary")


@_attrs_define
class ProjectRollbackSummary:
    """Progress and receipt IDs for a recorded project rollback or undo."""

    operation_id: UUID
    status: str
    """Current receipt status, either running, blocked, or complete."""
    cutovers_completed: int
    cutovers_total: int
    """Number of planned service cutovers excluding skipped services."""
    phase: str | Unset = UNSET
    undo_of: UUID | Unset = UNSET
    undo_operation_id: UUID | Unset = UNSET
    pause_requested_at: datetime.datetime | Unset = UNSET
    created_at: datetime.datetime | Unset = UNSET
    updated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        operation_id = str(self.operation_id)

        status = self.status

        cutovers_completed = self.cutovers_completed

        cutovers_total = self.cutovers_total

        phase = self.phase

        undo_of: str | Unset = UNSET
        if not isinstance(self.undo_of, Unset):
            undo_of = str(self.undo_of)

        undo_operation_id: str | Unset = UNSET
        if not isinstance(self.undo_operation_id, Unset):
            undo_operation_id = str(self.undo_operation_id)

        pause_requested_at: str | Unset = UNSET
        if not isinstance(self.pause_requested_at, Unset):
            pause_requested_at = self.pause_requested_at.isoformat()

        created_at: str | Unset = UNSET
        if not isinstance(self.created_at, Unset):
            created_at = self.created_at.isoformat()

        updated_at: str | Unset = UNSET
        if not isinstance(self.updated_at, Unset):
            updated_at = self.updated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "operation_id": operation_id,
                "status": status,
                "cutovers_completed": cutovers_completed,
                "cutovers_total": cutovers_total,
            }
        )
        if phase is not UNSET:
            field_dict["phase"] = phase
        if undo_of is not UNSET:
            field_dict["undo_of"] = undo_of
        if undo_operation_id is not UNSET:
            field_dict["undo_operation_id"] = undo_operation_id
        if pause_requested_at is not UNSET:
            field_dict["pause_requested_at"] = pause_requested_at
        if created_at is not UNSET:
            field_dict["created_at"] = created_at
        if updated_at is not UNSET:
            field_dict["updated_at"] = updated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        operation_id = UUID(d.pop("operation_id"))

        status = d.pop("status")

        cutovers_completed = d.pop("cutovers_completed")

        cutovers_total = d.pop("cutovers_total")

        phase = d.pop("phase", UNSET)

        _undo_of = d.pop("undo_of", UNSET)
        undo_of: UUID | Unset
        if isinstance(_undo_of, Unset):
            undo_of = UNSET
        else:
            undo_of = UUID(_undo_of)

        _undo_operation_id = d.pop("undo_operation_id", UNSET)
        undo_operation_id: UUID | Unset
        if isinstance(_undo_operation_id, Unset):
            undo_operation_id = UNSET
        else:
            undo_operation_id = UUID(_undo_operation_id)

        _pause_requested_at = d.pop("pause_requested_at", UNSET)
        pause_requested_at: datetime.datetime | Unset
        if isinstance(_pause_requested_at, Unset):
            pause_requested_at = UNSET
        else:
            pause_requested_at = datetime.datetime.fromisoformat(_pause_requested_at)

        _created_at = d.pop("created_at", UNSET)
        created_at: datetime.datetime | Unset
        if isinstance(_created_at, Unset):
            created_at = UNSET
        else:
            created_at = datetime.datetime.fromisoformat(_created_at)

        _updated_at = d.pop("updated_at", UNSET)
        updated_at: datetime.datetime | Unset
        if isinstance(_updated_at, Unset):
            updated_at = UNSET
        else:
            updated_at = datetime.datetime.fromisoformat(_updated_at)

        project_rollback_summary = cls(
            operation_id=operation_id,
            status=status,
            cutovers_completed=cutovers_completed,
            cutovers_total=cutovers_total,
            phase=phase,
            undo_of=undo_of,
            undo_operation_id=undo_operation_id,
            pause_requested_at=pause_requested_at,
            created_at=created_at,
            updated_at=updated_at,
        )

        project_rollback_summary.additional_properties = d
        return project_rollback_summary

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
