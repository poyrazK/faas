from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_execution_attention_target_status import (
    WorkflowRecoveryExecutionAttentionTargetStatus,
    check_workflow_recovery_execution_attention_target_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryExecutionAttentionTarget")


@_attrs_define
class WorkflowRecoveryExecutionAttentionTarget:
    """Advisory observation of one admitted native workflow generation with run and diagnostics links."""

    position: int
    run_id: UUID
    resume_count: int
    status: WorkflowRecoveryExecutionAttentionTargetStatus
    run_url: str
    diagnostics_url: str
    wait_reason: str | Unset = UNSET
    admitted_at: datetime.datetime | Unset = UNSET
    last_progress_at: datetime.datetime | Unset = UNSET
    deadline_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        position = self.position

        run_id = str(self.run_id)

        resume_count = self.resume_count

        status: str = self.status

        run_url = self.run_url

        diagnostics_url = self.diagnostics_url

        wait_reason = self.wait_reason

        admitted_at: str | Unset = UNSET
        if not isinstance(self.admitted_at, Unset):
            admitted_at = self.admitted_at.isoformat()

        last_progress_at: str | Unset = UNSET
        if not isinstance(self.last_progress_at, Unset):
            last_progress_at = self.last_progress_at.isoformat()

        deadline_at: str | Unset = UNSET
        if not isinstance(self.deadline_at, Unset):
            deadline_at = self.deadline_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "position": position,
                "run_id": run_id,
                "resume_count": resume_count,
                "status": status,
                "run_url": run_url,
                "diagnostics_url": diagnostics_url,
            }
        )
        if wait_reason is not UNSET:
            field_dict["wait_reason"] = wait_reason
        if admitted_at is not UNSET:
            field_dict["admitted_at"] = admitted_at
        if last_progress_at is not UNSET:
            field_dict["last_progress_at"] = last_progress_at
        if deadline_at is not UNSET:
            field_dict["deadline_at"] = deadline_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        position = d.pop("position")

        run_id = UUID(d.pop("run_id"))

        resume_count = d.pop("resume_count")

        status = check_workflow_recovery_execution_attention_target_status(d.pop("status"))

        run_url = d.pop("run_url")

        diagnostics_url = d.pop("diagnostics_url")

        wait_reason = d.pop("wait_reason", UNSET)

        _admitted_at = d.pop("admitted_at", UNSET)
        admitted_at: datetime.datetime | Unset
        if isinstance(_admitted_at, Unset):
            admitted_at = UNSET
        else:
            admitted_at = datetime.datetime.fromisoformat(_admitted_at)

        _last_progress_at = d.pop("last_progress_at", UNSET)
        last_progress_at: datetime.datetime | Unset
        if isinstance(_last_progress_at, Unset):
            last_progress_at = UNSET
        else:
            last_progress_at = datetime.datetime.fromisoformat(_last_progress_at)

        _deadline_at = d.pop("deadline_at", UNSET)
        deadline_at: datetime.datetime | Unset
        if isinstance(_deadline_at, Unset):
            deadline_at = UNSET
        else:
            deadline_at = datetime.datetime.fromisoformat(_deadline_at)

        workflow_recovery_execution_attention_target = cls(
            position=position,
            run_id=run_id,
            resume_count=resume_count,
            status=status,
            run_url=run_url,
            diagnostics_url=diagnostics_url,
            wait_reason=wait_reason,
            admitted_at=admitted_at,
            last_progress_at=last_progress_at,
            deadline_at=deadline_at,
        )

        workflow_recovery_execution_attention_target.additional_properties = d
        return workflow_recovery_execution_attention_target

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
