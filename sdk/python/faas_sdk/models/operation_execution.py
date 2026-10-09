from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

T = TypeVar("T", bound="OperationExecution")


@_attrs_define
class OperationExecution:
    """Retained execution generation. Exactly one invocation_id or workflow_run_id is present. Workflow attempts counts
    retained HTTP step attempts at that generation.

    """

    generation: int
    state: str
    attempts: int
    created_at: datetime.datetime
    invocation_id: UUID | Unset = UNSET
    job_run_id: UUID | Unset = UNSET
    workflow_run_id: UUID | Unset = UNSET
    completed_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        generation = self.generation

        state = self.state

        attempts = self.attempts

        created_at = self.created_at.isoformat()

        invocation_id: str | Unset = UNSET
        if not isinstance(self.invocation_id, Unset):
            invocation_id = str(self.invocation_id)

        job_run_id: str | Unset = UNSET
        if not isinstance(self.job_run_id, Unset):
            job_run_id = str(self.job_run_id)

        workflow_run_id: str | Unset = UNSET
        if not isinstance(self.workflow_run_id, Unset):
            workflow_run_id = str(self.workflow_run_id)

        completed_at: str | Unset = UNSET
        if not isinstance(self.completed_at, Unset):
            completed_at = self.completed_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "generation": generation,
                "state": state,
                "attempts": attempts,
                "created_at": created_at,
            }
        )
        if invocation_id is not UNSET:
            field_dict["invocation_id"] = invocation_id
        if job_run_id is not UNSET:
            field_dict["job_run_id"] = job_run_id
        if workflow_run_id is not UNSET:
            field_dict["workflow_run_id"] = workflow_run_id
        if completed_at is not UNSET:
            field_dict["completed_at"] = completed_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        generation = d.pop("generation")

        state = d.pop("state")

        attempts = d.pop("attempts")

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        _invocation_id = d.pop("invocation_id", UNSET)
        invocation_id: UUID | Unset
        if isinstance(_invocation_id, Unset):
            invocation_id = UNSET
        else:
            invocation_id = UUID(_invocation_id)

        _job_run_id = d.pop("job_run_id", UNSET)
        job_run_id: UUID | Unset
        if isinstance(_job_run_id, Unset):
            job_run_id = UNSET
        else:
            job_run_id = UUID(_job_run_id)

        _workflow_run_id = d.pop("workflow_run_id", UNSET)
        workflow_run_id: UUID | Unset
        if isinstance(_workflow_run_id, Unset):
            workflow_run_id = UNSET
        else:
            workflow_run_id = UUID(_workflow_run_id)

        _completed_at = d.pop("completed_at", UNSET)
        completed_at: datetime.datetime | Unset
        if isinstance(_completed_at, Unset):
            completed_at = UNSET
        else:
            completed_at = datetime.datetime.fromisoformat(_completed_at)

        operation_execution = cls(
            generation=generation,
            state=state,
            attempts=attempts,
            created_at=created_at,
            invocation_id=invocation_id,
            job_run_id=job_run_id,
            workflow_run_id=workflow_run_id,
            completed_at=completed_at,
        )

        operation_execution.additional_properties = d
        return operation_execution

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
