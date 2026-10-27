from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.operation_data_repair_phase import OperationDataRepairPhase, check_operation_data_repair_phase
from ..models.operation_data_repair_rehearsal_status import (
    OperationDataRepairRehearsalStatus,
    check_operation_data_repair_rehearsal_status,
)
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.operation_data_repair_approval import OperationDataRepairApproval


T = TypeVar("T", bound="OperationDataRepair")


@_attrs_define
class OperationDataRepair:
    """Repair plan metadata in Operation history; captured row values remain in the application executor."""

    source_operation_id: UUID
    plan_id: UUID
    plan_digest: str
    phase: OperationDataRepairPhase
    candidates: int
    conflicts: int
    unavailable: int
    plan_expires_at: datetime.datetime
    phase_started_at: datetime.datetime | Unset = UNSET
    """Omitted on legacy records; health observations conservatively use creation time."""
    approval: OperationDataRepairApproval | Unset = UNSET
    applied_at: datetime.datetime | Unset = UNSET
    rehearsal_task_id: UUID | Unset = UNSET
    rehearsal_status: OperationDataRepairRehearsalStatus | Unset = UNSET
    rehearsal_cancel_requested: bool | Unset = UNSET
    replaces_plan_id: UUID | Unset = UNSET
    """Original parent plan for a linked replacement; omitted on root plans."""
    replaces_plan_digest: str | Unset = UNSET
    """Exact frozen parent digest; approval authority is never inherited."""
    apply_closed_at: datetime.datetime | Unset = UNSET
    """Source-confirmed permanent closure without an apply commit. Replacement requires fresh rehearsal and
    approval."""
    replacement_operation_id: UUID | Unset = UNSET
    """Reserved replacement UUID. Resume the same identity after response loss."""
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        source_operation_id = str(self.source_operation_id)

        plan_id = str(self.plan_id)

        plan_digest = self.plan_digest

        phase: str = self.phase

        candidates = self.candidates

        conflicts = self.conflicts

        unavailable = self.unavailable

        plan_expires_at = self.plan_expires_at.isoformat()

        phase_started_at: str | Unset = UNSET
        if not isinstance(self.phase_started_at, Unset):
            phase_started_at = self.phase_started_at.isoformat()

        approval: dict[str, Any] | Unset = UNSET
        if not isinstance(self.approval, Unset):
            approval = self.approval.to_dict()

        applied_at: str | Unset = UNSET
        if not isinstance(self.applied_at, Unset):
            applied_at = self.applied_at.isoformat()

        rehearsal_task_id: str | Unset = UNSET
        if not isinstance(self.rehearsal_task_id, Unset):
            rehearsal_task_id = str(self.rehearsal_task_id)

        rehearsal_status: str | Unset = UNSET
        if not isinstance(self.rehearsal_status, Unset):
            rehearsal_status = self.rehearsal_status

        rehearsal_cancel_requested = self.rehearsal_cancel_requested

        replaces_plan_id: str | Unset = UNSET
        if not isinstance(self.replaces_plan_id, Unset):
            replaces_plan_id = str(self.replaces_plan_id)

        replaces_plan_digest = self.replaces_plan_digest

        apply_closed_at: str | Unset = UNSET
        if not isinstance(self.apply_closed_at, Unset):
            apply_closed_at = self.apply_closed_at.isoformat()

        replacement_operation_id: str | Unset = UNSET
        if not isinstance(self.replacement_operation_id, Unset):
            replacement_operation_id = str(self.replacement_operation_id)

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "source_operation_id": source_operation_id,
                "plan_id": plan_id,
                "plan_digest": plan_digest,
                "phase": phase,
                "candidates": candidates,
                "conflicts": conflicts,
                "unavailable": unavailable,
                "plan_expires_at": plan_expires_at,
            }
        )
        if phase_started_at is not UNSET:
            field_dict["phase_started_at"] = phase_started_at
        if approval is not UNSET:
            field_dict["approval"] = approval
        if applied_at is not UNSET:
            field_dict["applied_at"] = applied_at
        if rehearsal_task_id is not UNSET:
            field_dict["rehearsal_task_id"] = rehearsal_task_id
        if rehearsal_status is not UNSET:
            field_dict["rehearsal_status"] = rehearsal_status
        if rehearsal_cancel_requested is not UNSET:
            field_dict["rehearsal_cancel_requested"] = rehearsal_cancel_requested
        if replaces_plan_id is not UNSET:
            field_dict["replaces_plan_id"] = replaces_plan_id
        if replaces_plan_digest is not UNSET:
            field_dict["replaces_plan_digest"] = replaces_plan_digest
        if apply_closed_at is not UNSET:
            field_dict["apply_closed_at"] = apply_closed_at
        if replacement_operation_id is not UNSET:
            field_dict["replacement_operation_id"] = replacement_operation_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.operation_data_repair_approval import OperationDataRepairApproval

        d = dict(src_dict)
        source_operation_id = UUID(d.pop("source_operation_id"))

        plan_id = UUID(d.pop("plan_id"))

        plan_digest = d.pop("plan_digest")

        phase = check_operation_data_repair_phase(d.pop("phase"))

        candidates = d.pop("candidates")

        conflicts = d.pop("conflicts")

        unavailable = d.pop("unavailable")

        plan_expires_at = datetime.datetime.fromisoformat(d.pop("plan_expires_at"))

        _phase_started_at = d.pop("phase_started_at", UNSET)
        phase_started_at: datetime.datetime | Unset
        if isinstance(_phase_started_at, Unset):
            phase_started_at = UNSET
        else:
            phase_started_at = datetime.datetime.fromisoformat(_phase_started_at)

        _approval = d.pop("approval", UNSET)
        approval: OperationDataRepairApproval | Unset
        if isinstance(_approval, Unset):
            approval = UNSET
        else:
            approval = OperationDataRepairApproval.from_dict(_approval)

        _applied_at = d.pop("applied_at", UNSET)
        applied_at: datetime.datetime | Unset
        if isinstance(_applied_at, Unset):
            applied_at = UNSET
        else:
            applied_at = datetime.datetime.fromisoformat(_applied_at)

        _rehearsal_task_id = d.pop("rehearsal_task_id", UNSET)
        rehearsal_task_id: UUID | Unset
        if isinstance(_rehearsal_task_id, Unset):
            rehearsal_task_id = UNSET
        else:
            rehearsal_task_id = UUID(_rehearsal_task_id)

        _rehearsal_status = d.pop("rehearsal_status", UNSET)
        rehearsal_status: OperationDataRepairRehearsalStatus | Unset
        if isinstance(_rehearsal_status, Unset):
            rehearsal_status = UNSET
        else:
            rehearsal_status = check_operation_data_repair_rehearsal_status(_rehearsal_status)

        rehearsal_cancel_requested = d.pop("rehearsal_cancel_requested", UNSET)

        _replaces_plan_id = d.pop("replaces_plan_id", UNSET)
        replaces_plan_id: UUID | Unset
        if isinstance(_replaces_plan_id, Unset):
            replaces_plan_id = UNSET
        else:
            replaces_plan_id = UUID(_replaces_plan_id)

        replaces_plan_digest = d.pop("replaces_plan_digest", UNSET)

        _apply_closed_at = d.pop("apply_closed_at", UNSET)
        apply_closed_at: datetime.datetime | Unset
        if isinstance(_apply_closed_at, Unset):
            apply_closed_at = UNSET
        else:
            apply_closed_at = datetime.datetime.fromisoformat(_apply_closed_at)

        _replacement_operation_id = d.pop("replacement_operation_id", UNSET)
        replacement_operation_id: UUID | Unset
        if isinstance(_replacement_operation_id, Unset):
            replacement_operation_id = UNSET
        else:
            replacement_operation_id = UUID(_replacement_operation_id)

        operation_data_repair = cls(
            source_operation_id=source_operation_id,
            plan_id=plan_id,
            plan_digest=plan_digest,
            phase=phase,
            candidates=candidates,
            conflicts=conflicts,
            unavailable=unavailable,
            plan_expires_at=plan_expires_at,
            phase_started_at=phase_started_at,
            approval=approval,
            applied_at=applied_at,
            rehearsal_task_id=rehearsal_task_id,
            rehearsal_status=rehearsal_status,
            rehearsal_cancel_requested=rehearsal_cancel_requested,
            replaces_plan_id=replaces_plan_id,
            replaces_plan_digest=replaces_plan_digest,
            apply_closed_at=apply_closed_at,
            replacement_operation_id=replacement_operation_id,
        )

        operation_data_repair.additional_properties = d
        return operation_data_repair

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
