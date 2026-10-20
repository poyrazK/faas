from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_recovery_cancellation_response_outcome import (
    WorkflowRecoveryCancellationResponseOutcome,
    check_workflow_recovery_cancellation_response_outcome,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowRecoveryCancellationResponse")


@_attrs_define
class WorkflowRecoveryCancellationResponse:
    """Read-only preview or cancellation result. Only eligible generations can be cancelled. Repeating a cancellation
    against the same cancelled generation does not append another history entry.

    """

    job_id: UUID
    position: int
    resume_count: int
    outcome: WorkflowRecoveryCancellationResponseOutcome
    preview: bool
    run_id: UUID | Unset = UNSET
    status: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        job_id = str(self.job_id)

        position = self.position

        resume_count = self.resume_count

        outcome: str = self.outcome

        preview = self.preview

        run_id: str | Unset = UNSET
        if not isinstance(self.run_id, Unset):
            run_id = str(self.run_id)

        status = self.status

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "job_id": job_id,
                "position": position,
                "resume_count": resume_count,
                "outcome": outcome,
                "preview": preview,
            }
        )
        if run_id is not UNSET:
            field_dict["run_id"] = run_id
        if status is not UNSET:
            field_dict["status"] = status

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        job_id = UUID(d.pop("job_id"))

        position = d.pop("position")

        resume_count = d.pop("resume_count")

        outcome = check_workflow_recovery_cancellation_response_outcome(d.pop("outcome"))

        preview = d.pop("preview")

        _run_id = d.pop("run_id", UNSET)
        run_id: UUID | Unset
        if isinstance(_run_id, Unset):
            run_id = UNSET
        else:
            run_id = UUID(_run_id)

        status = d.pop("status", UNSET)

        workflow_recovery_cancellation_response = cls(
            job_id=job_id,
            position=position,
            resume_count=resume_count,
            outcome=outcome,
            preview=preview,
            run_id=run_id,
            status=status,
        )

        workflow_recovery_cancellation_response.additional_properties = d
        return workflow_recovery_cancellation_response

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
