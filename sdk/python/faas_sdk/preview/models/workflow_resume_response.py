from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.workflow_resume_response_previous_status import (
    WorkflowResumeResponsePreviousStatus,
    check_workflow_resume_response_previous_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="WorkflowResumeResponse")


@_attrs_define
class WorkflowResumeResponse:
    """Immutable record of one accepted continuation request."""

    run_id: UUID
    resume_number: int
    account_id: UUID
    previous_status: WorkflowResumeResponsePreviousStatus
    resumed_steps: list[str]
    created_at: datetime.datetime
    previous_error: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        resume_number = self.resume_number

        account_id = str(self.account_id)

        previous_status: str = self.previous_status

        resumed_steps = self.resumed_steps

        created_at = self.created_at.isoformat()

        previous_error = self.previous_error

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "resume_number": resume_number,
                "account_id": account_id,
                "previous_status": previous_status,
                "resumed_steps": resumed_steps,
                "created_at": created_at,
            }
        )
        if previous_error is not UNSET:
            field_dict["previous_error"] = previous_error

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        resume_number = d.pop("resume_number")

        account_id = UUID(d.pop("account_id"))

        previous_status = check_workflow_resume_response_previous_status(d.pop("previous_status"))

        resumed_steps = cast(list[str], d.pop("resumed_steps"))

        created_at = datetime.datetime.fromisoformat(d.pop("created_at"))

        previous_error = d.pop("previous_error", UNSET)

        workflow_resume_response = cls(
            run_id=run_id,
            resume_number=resume_number,
            account_id=account_id,
            previous_status=previous_status,
            resumed_steps=resumed_steps,
            created_at=created_at,
            previous_error=previous_error,
        )

        workflow_resume_response.additional_properties = d
        return workflow_resume_response

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
