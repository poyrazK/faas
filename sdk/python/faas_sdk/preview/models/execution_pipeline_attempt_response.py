from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..models.execution_pipeline_attempt_response_status import (
    ExecutionPipelineAttemptResponseStatus,
    check_execution_pipeline_attempt_response_status,
)
from ..types import UNSET, Unset

T = TypeVar("T", bound="ExecutionPipelineAttemptResponse")


@_attrs_define
class ExecutionPipelineAttemptResponse:
    """Runtime state and linked Run receipt for a pipeline attempt."""

    run_id: UUID
    attempt: int
    status: ExecutionPipelineAttemptResponseStatus
    previous_run_id: UUID | Unset = UNSET
    failure_code: str | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        run_id = str(self.run_id)

        attempt = self.attempt

        status: str = self.status

        previous_run_id: str | Unset = UNSET
        if not isinstance(self.previous_run_id, Unset):
            previous_run_id = str(self.previous_run_id)

        failure_code = self.failure_code

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update(
            {
                "run_id": run_id,
                "attempt": attempt,
                "status": status,
            }
        )
        if previous_run_id is not UNSET:
            field_dict["previous_run_id"] = previous_run_id
        if failure_code is not UNSET:
            field_dict["failure_code"] = failure_code

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        run_id = UUID(d.pop("run_id"))

        attempt = d.pop("attempt")

        status = check_execution_pipeline_attempt_response_status(d.pop("status"))

        _previous_run_id = d.pop("previous_run_id", UNSET)
        previous_run_id: UUID | Unset
        if isinstance(_previous_run_id, Unset):
            previous_run_id = UNSET
        else:
            previous_run_id = UUID(_previous_run_id)

        failure_code = d.pop("failure_code", UNSET)

        execution_pipeline_attempt_response = cls(
            run_id=run_id,
            attempt=attempt,
            status=status,
            previous_run_id=previous_run_id,
            failure_code=failure_code,
        )

        execution_pipeline_attempt_response.additional_properties = d
        return execution_pipeline_attempt_response

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
